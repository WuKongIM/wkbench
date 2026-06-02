package metricscollector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
)

type collector struct {
	env      contract.RunEnv
	target   targetport.Target
	spec     collectorSpec
	filter   metricFilter
	artifact io.WriteCloser
	client   *http.Client

	cancelOnce sync.Once
	cancel     context.CancelFunc
	done       chan error
	stopped    chan struct{}

	finalizeOnce sync.Once
	finalizeErr  error

	state collectorState
}

type collectorState struct {
	scrapeTicks        int64
	selectedSamples    int64
	droppedMetricNames int64
	nodes              map[string]*wukongimport.NodeScrapeSummary
	latencies          []time.Duration
	latest             map[string]metricSample
	droppedSeries      map[string]struct{}
}

type scrapeRecord struct {
	Timestamp  string         `json:"timestamp"`
	NodeIndex  int            `json:"node_index"`
	Address    string         `json:"address"`
	DurationMS float64        `json:"duration_ms"`
	Status     string         `json:"status"`
	StatusCode int            `json:"status_code,omitempty"`
	Error      string         `json:"error,omitempty"`
	Samples    []metricSample `json:"samples,omitempty"`
}

type nodeScrapeResult struct {
	record      scrapeRecord
	samples     []metricSample
	parseErrors int64
	err         error
}

func newCollector(env contract.RunEnv, target targetport.Target, spec collectorSpec, filter metricFilter, artifact io.WriteCloser) *collector {
	return &collector{
		env:      env,
		target:   target,
		spec:     spec,
		filter:   filter,
		artifact: artifact,
		client:   &http.Client{},
		done:     make(chan error, 1),
		stopped:  make(chan struct{}),
		state: collectorState{
			nodes:         make(map[string]*wukongimport.NodeScrapeSummary),
			latest:        make(map[string]metricSample),
			droppedSeries: make(map[string]struct{}),
		},
	}
}

func (c *collector) start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	go c.run(ctx)
}

func (c *collector) Done() <-chan error { return c.done }

func (c *collector) Stop(ctx context.Context) error {
	c.cancelOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
	})

	select {
	case <-c.stopped:
	case <-ctx.Done():
		return ctx.Err()
	}

	c.finalizeOnce.Do(func() {
		closeErr := c.artifact.Close()
		outputErr := c.env.SetOutput("summary", c.summary())
		if closeErr != nil {
			c.finalizeErr = closeErr
			return
		}
		c.finalizeErr = outputErr
	})
	return c.finalizeErr
}

func (c *collector) run(ctx context.Context) {
	defer close(c.stopped)

	fatal := c.loop(ctx)
	if fatal != nil {
		c.done <- fatal
	}
	close(c.done)
}

func (c *collector) loop(ctx context.Context) error {
	consecutiveErrorTicks := 0
	for {
		hadScrapeError, err := c.scrapeTick(ctx)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		if hadScrapeError {
			consecutiveErrorTicks++
			if c.spec.FailOnScrapeError {
				limit := c.spec.MaxConsecutiveErrors
				if limit == 0 || consecutiveErrorTicks >= limit {
					return fmt.Errorf("scrape error threshold reached after %d consecutive tick(s)", consecutiveErrorTicks)
				}
			}
		} else {
			consecutiveErrorTicks = 0
		}

		timer := time.NewTimer(c.spec.Interval.Duration)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		}
	}
}

func (c *collector) scrapeTick(ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	c.state.scrapeTicks++

	results := make([]nodeScrapeResult, len(c.target.APIAddrs))
	var wg sync.WaitGroup
	wg.Add(len(c.target.APIAddrs))
	for index, addr := range c.target.APIAddrs {
		go func(index int, addr string) {
			defer wg.Done()
			results[index] = c.scrapeNode(ctx, index, addr)
		}(index, addr)
	}
	wg.Wait()

	if ctx.Err() != nil {
		return false, nil
	}

	hadScrapeError := false
	for index, result := range results {
		if err := c.writeRecord(result.record); err != nil {
			return hadScrapeError, fmt.Errorf("write metrics artifact: %w", err)
		}
		addr := c.target.APIAddrs[index]
		c.recordResult(addr, result)
		if result.err != nil || result.parseErrors > 0 {
			hadScrapeError = true
		}
	}
	return hadScrapeError, nil
}

func (c *collector) scrapeNode(ctx context.Context, index int, addr string) nodeScrapeResult {
	started := time.Now()
	reqCtx, cancel := context.WithTimeout(ctx, c.spec.Timeout.Duration)
	defer cancel()

	record := scrapeRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		NodeIndex: index,
		Address:   addr,
		Status:    "success",
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, metricsURL(addr, c.spec.Path), nil)
	if err != nil {
		record.DurationMS = durationMS(time.Since(started))
		record.Status = "error"
		record.Error = err.Error()
		return nodeScrapeResult{record: record, err: err}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		record.DurationMS = durationMS(time.Since(started))
		record.Status = "error"
		record.Error = err.Error()
		return nodeScrapeResult{record: record, err: err}
	}
	defer resp.Body.Close()
	record.StatusCode = resp.StatusCode

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("HTTP %d", resp.StatusCode)
		record.DurationMS = durationMS(time.Since(started))
		record.Status = "error"
		record.Error = err.Error()
		return nodeScrapeResult{record: record, err: err}
	}

	samples, parseErrors, err := parsePrometheusReader(resp.Body, c.filter)
	if err != nil {
		record.DurationMS = durationMS(time.Since(started))
		record.Status = "error"
		record.Error = err.Error()
		return nodeScrapeResult{record: record, err: err}
	}
	sortMetricSamples(samples)
	record.DurationMS = durationMS(time.Since(started))
	record.Samples = samples
	return nodeScrapeResult{record: record, samples: samples, parseErrors: parseErrors}
}

func (c *collector) writeRecord(record scrapeRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.artifact.Write(data)
	return err
}

func (c *collector) recordResult(addr string, result nodeScrapeResult) {
	labels := contract.Labels{
		"address":    addr,
		"node_index": fmt.Sprintf("%d", result.record.NodeIndex),
	}
	c.env.ObserveDuration("scrape_latency", time.Duration(result.record.DurationMS*float64(time.Millisecond)), labels)
	c.state.latencies = append(c.state.latencies, time.Duration(result.record.DurationMS*float64(time.Millisecond)))

	node := c.node(addr)
	if result.err != nil {
		node.Errors++
		c.env.EmitCounter("scrape_error_total", 1, labels)
		return
	}

	node.Success++
	c.env.EmitCounter("scrape_success_total", 1, labels)
	if result.parseErrors > 0 {
		c.env.EmitCounter("scrape_parse_error_total", float64(result.parseErrors), labels)
	}
	c.state.selectedSamples += int64(len(result.samples))
	c.recordLatest(result.samples)
}

func (c *collector) node(addr string) *wukongimport.NodeScrapeSummary {
	node, ok := c.state.nodes[addr]
	if !ok {
		node = &wukongimport.NodeScrapeSummary{Address: addr}
		c.state.nodes[addr] = node
	}
	return node
}

func (c *collector) recordLatest(samples []metricSample) {
	for _, sample := range samples {
		key := sampleSeriesKey(sample)
		if _, ok := c.state.latest[key]; ok {
			c.state.latest[key] = cloneMetricSample(sample)
			continue
		}
		if _, ok := c.state.droppedSeries[key]; ok {
			continue
		}
		if len(c.state.latest) < c.spec.MaxSummaryMetrics {
			c.state.latest[key] = cloneMetricSample(sample)
			continue
		}
		c.state.droppedSeries[key] = struct{}{}
		c.state.droppedMetricNames++
	}
}

func (c *collector) summary() wukongimport.MetricsSummary {
	nodes := make([]wukongimport.NodeScrapeSummary, 0, len(c.state.nodes))
	for _, node := range c.state.nodes {
		nodes = append(nodes, *node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Address < nodes[j].Address
	})

	latestSamples := make([]metricSample, 0, len(c.state.latest))
	for _, sample := range c.state.latest {
		latestSamples = append(latestSamples, cloneMetricSample(sample))
	}
	sortMetricSamples(latestSamples)
	latest := make([]wukongimport.MetricSampleSummary, 0, len(latestSamples))
	for _, sample := range latestSamples {
		latest = append(latest, wukongimport.MetricSampleSummary{
			Name:   sample.Name,
			Labels: cloneLabels(sample.Labels),
			Value:  sample.Value,
		})
	}

	return wukongimport.MetricsSummary{
		ScrapeTicks:        c.state.scrapeTicks,
		SelectedSamples:    c.state.selectedSamples,
		DroppedMetricNames: c.state.droppedMetricNames,
		Nodes:              nodes,
		LatencyP95MS:       percentileMS(c.state.latencies, 0.95),
		LatencyP99MS:       percentileMS(c.state.latencies, 0.99),
		Latest:             latest,
	}
}

func metricsURL(addr, path string) string {
	return strings.TrimRight(addr, "/") + "/" + strings.TrimLeft(path, "/")
}

func sortMetricSamples(samples []metricSample) {
	sort.Slice(samples, func(i, j int) bool {
		return compareMetricSamples(samples[i], samples[j]) < 0
	})
}

func compareMetricSamples(a, b metricSample) int {
	if a.Name < b.Name {
		return -1
	}
	if a.Name > b.Name {
		return 1
	}
	aLabels := labelsKey(a.Labels)
	bLabels := labelsKey(b.Labels)
	if aLabels < bLabels {
		return -1
	}
	if aLabels > bLabels {
		return 1
	}
	return 0
}

type seriesLabelPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func sampleSeriesKey(sample metricSample) string {
	payload := struct {
		Name   string            `json:"name"`
		Labels []seriesLabelPair `json:"labels"`
	}{
		Name:   sample.Name,
		Labels: sortedLabelPairs(sample.Labels),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return sample.Name
	}
	return string(data)
}

func labelsKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	data, err := json.Marshal(sortedLabelPairs(labels))
	if err != nil {
		return ""
	}
	return string(data)
}

func sortedLabelPairs(labels map[string]string) []seriesLabelPair {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]seriesLabelPair, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, seriesLabelPair{Name: key, Value: labels[key]})
	}
	return pairs
}

func percentileMS(values []time.Duration, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	index := int(percentile*float64(len(sorted))+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return durationMS(sorted[index])
}

func durationMS(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func cloneMetricSample(sample metricSample) metricSample {
	return metricSample{
		Name:   sample.Name,
		Labels: cloneLabels(sample.Labels),
		Value:  sample.Value,
	}
}

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
