package metricscollector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
)

func TestUnitContractAndDefinitionShape(t *testing.T) {
	unit := Unit{}
	unittest.AssertUnitContract(t, unit)

	def := unit.Definition()
	if def.Kind != Kind {
		t.Fatalf("kind = %q, want %q", def.Kind, Kind)
	}
	if len(def.Inputs) != 1 || def.Inputs[0].Name != "target" || def.Inputs[0].Type != targetport.TargetV1 {
		t.Fatalf("unexpected inputs: %#v", def.Inputs)
	}
	if len(def.Outputs) != 1 || def.Outputs[0].Name != "summary" || def.Outputs[0].Type != wukongimport.MetricsSummaryV1 {
		t.Fatalf("unexpected outputs: %#v", def.Outputs)
	}
	assertMetric(t, def.Metrics, "scrape_success_total", "counter")
	assertMetric(t, def.Metrics, "scrape_error_total", "counter")
	assertMetric(t, def.Metrics, "scrape_parse_error_total", "counter")
	assertMetric(t, def.Metrics, "scrape_latency", "duration")
	if len(def.Artifacts) != 1 || def.Artifacts[0].Name != "metrics.jsonl" || def.Artifacts[0].ContentType != "application/jsonl" {
		t.Fatalf("unexpected artifacts: %#v", def.Artifacts)
	}
}

func TestValidateRejectsInvalidSpec(t *testing.T) {
	tests := []struct {
		name string
		spec map[string]any
		want string
	}{
		{name: "interval", spec: map[string]any{"interval": "0s"}, want: "interval"},
		{name: "timeout", spec: map[string]any{"timeout": "0s"}, want: "timeout"},
		{name: "empty path", spec: map[string]any{"path": ""}, want: "path"},
		{name: "relative path", spec: map[string]any{"path": "metrics"}, want: "path"},
		{name: "include regex", spec: map[string]any{"include": []string{"["}}, want: "include"},
		{name: "exclude regex", spec: map[string]any{"exclude": []string{"["}}, want: "exclude"},
		{name: "max consecutive errors", spec: map[string]any{"max_consecutive_errors": -1}, want: "max_consecutive_errors"},
		{name: "max summary metrics", spec: map[string]any{"max_summary_metrics": -1}, want: "max_summary_metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Unit{}.Validate(context.Background(), newEnv(tt.spec))
			if err == nil {
				t.Fatalf("Validate returned nil, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want mention %q", err, tt.want)
			}
		})
	}
}

func TestValidateAcceptsDefaultsAndPlanUsesIntervalAndPath(t *testing.T) {
	unit := Unit{}
	env := newEnv(map[string]any{"max_summary_metrics": 0})
	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}

	plan, err := unit.Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.UnitName != "collector" {
		t.Fatalf("unit name = %q", plan.UnitName)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("shards = %#v, want one", plan.Shards)
	}
	shard, ok := plan.Shards[0].(planShard)
	if !ok {
		t.Fatalf("shard type = %T, want planShard", plan.Shards[0])
	}
	if shard.Interval != "1s" || shard.Path != "/metrics" {
		t.Fatalf("unexpected shard: %#v", shard)
	}

	spec, err := decodeSpec(env)
	if err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	if spec.Timeout.Duration != 800*time.Millisecond || spec.MaxSummaryMetrics != 100 {
		t.Fatalf("defaults not normalized: %#v", spec)
	}
}

func TestRunReturnsBackgroundPlaceholder(t *testing.T) {
	err := Unit{}.Run(context.Background(), newEnv(nil))
	if err == nil {
		t.Fatalf("Run returned nil, want background-unit error")
	}
	if !strings.Contains(err.Error(), Kind) || !strings.Contains(err.Error(), "use Start") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestMetricFilterAppliesIncludeThenExclude(t *testing.T) {
	filter, err := newMetricFilter(collectorSpec{
		Include: []string{"^wk_.*", "^go_.*"},
		Exclude: []string{".*_bucket$"},
	})
	if err != nil {
		t.Fatalf("new filter: %v", err)
	}

	samples, parseErrors := parsePrometheusText([]byte(`
# HELP ignored comments
wk_messages_total 12
go_threads 5
process_cpu_seconds_total 1
wk_latency_bucket 99
`), filter)
	if parseErrors != 0 {
		t.Fatalf("parse errors = %d", parseErrors)
	}
	if got := sampleNames(samples); strings.Join(got, ",") != "wk_messages_total,go_threads" {
		t.Fatalf("sample names = %#v", got)
	}
}

func TestParsePrometheusTextParsesLabelsAndMalformedLines(t *testing.T) {
	samples, parseErrors := parsePrometheusText([]byte(`
wk_channel_online{node="1",role="leader"} 3.5
bad no-number
wk_no_labels 2
malformed{node="1" 4
wk_bad_label{node} 7
wk_empty_label{="missing"} 8
wk_unquoted_label{node=1} 9
wk_invalid_label_key{bad-label="1"} 10
wk_extra_brace{{node="1"} 11
wk_duplicate_label{node="1",node="2"} 12
`), metricFilter{})
	if parseErrors != 8 {
		t.Fatalf("parse errors = %d, want 8", parseErrors)
	}
	if len(samples) != 2 {
		t.Fatalf("samples = %#v, want 2", samples)
	}
	if samples[0].Name != "wk_channel_online" || samples[0].Value != 3.5 || samples[0].Labels["node"] != "1" || samples[0].Labels["role"] != "leader" {
		t.Fatalf("first sample = %#v", samples[0])
	}
	if samples[1].Name != "wk_no_labels" || len(samples[1].Labels) != 0 || samples[1].Value != 2 {
		t.Fatalf("second sample = %#v", samples[1])
	}
}

func TestParsePrometheusTextParsesQuotedLabelValuesWithSpacesCommasAndEscapes(t *testing.T) {
	samples, parseErrors := parsePrometheusText([]byte(`
wk_space_label{label="a b"} 1
wk_comma_label{label="a,b"} 2
wk_escape_label{label="a\"b",path="c\\d"} 3
`), metricFilter{})
	if parseErrors != 0 {
		t.Fatalf("parse errors = %d, want 0", parseErrors)
	}
	if len(samples) != 3 {
		t.Fatalf("samples = %#v, want 3", samples)
	}
	if samples[0].Name != "wk_space_label" || samples[0].Labels["label"] != "a b" || samples[0].Value != 1 {
		t.Fatalf("space label sample = %#v", samples[0])
	}
	if samples[1].Name != "wk_comma_label" || samples[1].Labels["label"] != "a,b" || samples[1].Value != 2 {
		t.Fatalf("comma label sample = %#v", samples[1])
	}
	if samples[2].Name != "wk_escape_label" || samples[2].Labels["label"] != `a"b` || samples[2].Labels["path"] != `c\d` || samples[2].Value != 3 {
		t.Fatalf("escaped label sample = %#v", samples[2])
	}
}

func TestParsePrometheusTextRejectsNonFiniteValues(t *testing.T) {
	samples, parseErrors := parsePrometheusText([]byte(`
wk_nan NaN
wk_pos_inf +Inf
wk_neg_inf -Inf
wk_finite 1.5
`), metricFilter{})
	if parseErrors != 3 {
		t.Fatalf("parse errors = %d, want 3", parseErrors)
	}
	if len(samples) != 1 || samples[0].Name != "wk_finite" || samples[0].Value != 1.5 {
		t.Fatalf("samples = %#v, want only finite sample", samples)
	}
}

func TestParsePrometheusTextValidatesTrailingTokens(t *testing.T) {
	samples, parseErrors := parsePrometheusText([]byte(`
wk_no_timestamp 1
wk_with_timestamp 2 123
wk_extra_token 3 garbage
wk_too_many_tokens 4 123 bad
wk_bad_timestamp 5 not-an-int
`), metricFilter{})
	if parseErrors != 3 {
		t.Fatalf("parse errors = %d, want 3", parseErrors)
	}
	if len(samples) != 2 {
		t.Fatalf("samples = %#v, want 2", samples)
	}
	if samples[0].Name != "wk_no_timestamp" || samples[0].Value != 1 {
		t.Fatalf("first sample = %#v", samples[0])
	}
	if samples[1].Name != "wk_with_timestamp" || samples[1].Value != 2 {
		t.Fatalf("second sample = %#v", samples[1])
	}
}

func TestNewMetricFilterReportsRegexSide(t *testing.T) {
	_, err := newMetricFilter(collectorSpec{Include: []string{"["}})
	if err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("include error = %v", err)
	}
	_, err = newMetricFilter(collectorSpec{Exclude: []string{"["}})
	if err == nil || !strings.Contains(err.Error(), "exclude") {
		t.Fatalf("exclude error = %v", err)
	}
}

func TestMetricSampleMarshalsWithLowercaseKeys(t *testing.T) {
	data, err := json.Marshal(metricSample{
		Name:   "wk_messages_total",
		Labels: map[string]string{"node": "1"},
		Value:  12,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"name":"wk_messages_total","labels":{"node":"1"},"value":12}` {
		t.Fatalf("json = %s", data)
	}
}

func TestStartScrapesAndPublishesSummaryCountersMetricsAndArtifact(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Fatalf("path = %q, want /metrics", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`
wk_messages_total{node="a"} 12
wk_channels 3
ignored_metric 9
bad no-number
`))
		select {
		case requested <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	env := newCollectorEnv(map[string]any{
		"interval": "1h",
		"include":  []string{"^wk_"},
	}, targetport.Target{APIAddrs: []string{server.URL}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForRequest(t, requested)
	waitForCounter(t, env, "scrape_success_total", 1)
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	summary := outputSummary(t, env)
	if summary.ScrapeTicks != 1 {
		t.Fatalf("scrape ticks = %d, want 1", summary.ScrapeTicks)
	}
	if summary.SelectedSamples != 2 {
		t.Fatalf("selected samples = %d, want 2", summary.SelectedSamples)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].Address != server.URL || summary.Nodes[0].Success != 1 || summary.Nodes[0].Errors != 0 {
		t.Fatalf("nodes = %#v", summary.Nodes)
	}
	if got := sampleSummaryNames(summary.Latest); strings.Join(got, ",") != "wk_channels,wk_messages_total" {
		t.Fatalf("latest names = %#v", got)
	}
	if got := env.CounterValue("scrape_success_total"); got != 1 {
		t.Fatalf("success counter = %v, want 1", got)
	}
	if got := env.CounterValue("scrape_error_total"); got != 0 {
		t.Fatalf("error counter = %v, want 0", got)
	}
	if got := env.CounterValue("scrape_parse_error_total"); got != 1 {
		t.Fatalf("parse error counter = %v, want 1", got)
	}
	if got := env.DurationValues("scrape_latency"); len(got) != 1 || got[0] < 0 {
		t.Fatalf("latencies = %#v, want one nonnegative duration", got)
	}
	artifacts := env.Artifacts()
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v, want one", artifacts)
	}
	if info := artifacts["metrics.jsonl"]; info.Path == "" || info.ContentType != "application/jsonl" || info.SizeBytes == 0 {
		t.Fatalf("artifact info = %#v", info)
	}
}

func TestArtifactJSONLContainsLowercaseSampleFields(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`wk_messages_total{node="a"} 12` + "\n"))
		select {
		case requested <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	env := newCollectorEnv(map[string]any{"interval": "1h"}, targetport.Target{APIAddrs: []string{server.URL}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForRequest(t, requested)
	waitForCounter(t, env, "scrape_success_total", 1)
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info := env.Artifacts()["metrics.jsonl"]
	data, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("artifact lines = %q, want one line", data)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("artifact line is not JSON: %v\n%s", err, lines[0])
	}
	samples, ok := record["samples"].([]any)
	if !ok || len(samples) != 1 {
		t.Fatalf("samples = %#v, want one sample array", record["samples"])
	}
	sample, ok := samples[0].(map[string]any)
	if !ok {
		t.Fatalf("sample = %#v", samples[0])
	}
	for _, key := range []string{"name", "labels", "value"} {
		if _, ok := sample[key]; !ok {
			t.Fatalf("sample missing lower-case key %q: %#v", key, sample)
		}
	}
}

func TestNonStrictScrapeErrorsAreCountedAndNonfatal(t *testing.T) {
	env := newCollectorEnv(map[string]any{
		"interval": "1h",
		"timeout":  "20ms",
	}, targetport.Target{APIAddrs: []string{"http://127.0.0.1:1"}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForCounter(t, env, "scrape_error_total", 1)
	assertNoFatal(t, task.Done(), 40*time.Millisecond)
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	summary := outputSummary(t, env)
	if summary.ScrapeTicks != 1 {
		t.Fatalf("scrape ticks = %d, want 1", summary.ScrapeTicks)
	}
	if got := env.CounterValue("scrape_error_total"); got != 1 {
		t.Fatalf("error counter = %v, want 1", got)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].Errors != 1 || summary.Nodes[0].Success != 0 {
		t.Fatalf("nodes = %#v", summary.Nodes)
	}
}

func TestStrictScrapeErrorBecomesFatal(t *testing.T) {
	env := newCollectorEnv(map[string]any{
		"interval":               "1h",
		"timeout":                "20ms",
		"fail_on_scrape_error":   true,
		"max_consecutive_errors": 0,
		"max_summary_metrics":    3,
	}, targetport.Target{APIAddrs: []string{"http://127.0.0.1:1"}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	fatal := waitForFatal(t, task.Done())
	if !strings.Contains(fatal.Error(), "scrape") {
		t.Fatalf("fatal error = %v, want scrape", fatal)
	}
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after fatal: %v", err)
	}
}

func TestStopAfterFatalPublishesSummaryAndClosesArtifact(t *testing.T) {
	env := newCollectorEnv(map[string]any{
		"interval":               "1h",
		"timeout":                "20ms",
		"fail_on_scrape_error":   true,
		"max_consecutive_errors": 1,
	}, targetport.Target{APIAddrs: []string{"http://127.0.0.1:1"}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = waitForFatal(t, task.Done())
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after fatal: %v", err)
	}

	summary := outputSummary(t, env)
	if summary.ScrapeTicks != 1 || len(summary.Nodes) != 1 || summary.Nodes[0].Errors != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if info := env.Artifacts()["metrics.jsonl"]; info.Path == "" || info.SizeBytes == 0 {
		t.Fatalf("artifact info = %#v", info)
	}
}

func TestMaxSummaryMetricsCapsLatestAndCountsDroppedMetricNames(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
wk_c{node="1"} 3
wk_a{node="1"} 1
wk_b{node="1"} 2
`))
		select {
		case requested <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	env := newCollectorEnv(map[string]any{
		"interval":            "1h",
		"max_summary_metrics": 2,
	}, targetport.Target{APIAddrs: []string{server.URL}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForRequest(t, requested)
	waitForCounter(t, env, "scrape_success_total", 1)
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	summary := outputSummary(t, env)
	if len(summary.Latest) != 2 {
		t.Fatalf("latest = %#v, want cap of 2", summary.Latest)
	}
	if summary.DroppedMetricNames != 1 {
		t.Fatalf("dropped metric names = %d, want 1", summary.DroppedMetricNames)
	}
	if got := sampleSummaryNames(summary.Latest); strings.Join(got, ",") != "wk_a,wk_b" {
		t.Fatalf("latest names = %#v, want deterministic capped order", got)
	}
}

func TestLatencyPercentilesAreNonnegativeMilliseconds(t *testing.T) {
	var requests int64
	requested := make(chan struct{}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requests, 1)
		_, _ = w.Write([]byte("wk_messages_total 1\n"))
		select {
		case requested <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	env := newCollectorEnv(map[string]any{"interval": "10ms"}, targetport.Target{APIAddrs: []string{server.URL}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForRequests(t, requested, 3)
	waitForCounter(t, env, "scrape_success_total", 3)
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	summary := outputSummary(t, env)
	if summary.LatencyP95MS < 0 || summary.LatencyP99MS < 0 {
		t.Fatalf("latency summary = p95 %.3f p99 %.3f, want nonnegative", summary.LatencyP95MS, summary.LatencyP99MS)
	}
	if summary.LatencyP99MS > 1000 {
		t.Fatalf("latency p99 = %.3fms, want millisecond units", summary.LatencyP99MS)
	}
	if got := atomic.LoadInt64(&requests); got < 3 {
		t.Fatalf("requests = %d, want at least 3", got)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wk_messages_total 1\n"))
		select {
		case requested <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	env := newCollectorEnv(map[string]any{"interval": "1h"}, targetport.Target{APIAddrs: []string{server.URL}})
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForRequest(t, requested)
	waitForCounter(t, env, "scrape_success_total", 1)
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	outputSummary(t, env)
}

func assertMetric(t *testing.T, metrics []contract.MetricDef, name, typ string) {
	t.Helper()
	for _, metric := range metrics {
		if metric.Name == name {
			if metric.Type != typ {
				t.Fatalf("metric %q type = %q, want %q", name, metric.Type, typ)
			}
			return
		}
	}
	t.Fatalf("metric %q missing from %#v", name, metrics)
}

func sampleNames(samples []metricSample) []string {
	names := make([]string, 0, len(samples))
	for _, sample := range samples {
		names = append(names, sample.Name)
	}
	return names
}

func sampleSummaryNames(samples []wukongimport.MetricSampleSummary) []string {
	names := make([]string, 0, len(samples))
	for _, sample := range samples {
		names = append(names, sample.Name)
	}
	sort.Strings(names)
	return names
}

func newEnv(spec map[string]any) *contract.TestRunEnv {
	return contract.NewTestRunEnv("run-1", "collector", nil, spec)
}

func newCollectorEnv(spec map[string]any, target targetport.Target) *contract.TestRunEnv {
	env := contract.NewTestRunEnv("run-1", "collector", map[string]any{"target": target}, spec)
	env.DeclareArtifacts(Unit{}.Definition().Artifacts)
	return env
}

func outputSummary(t *testing.T, env *contract.TestRunEnv) wukongimport.MetricsSummary {
	t.Helper()
	output, ok := env.Output("summary")
	if !ok {
		t.Fatalf("summary output missing")
	}
	summary, ok := output.(wukongimport.MetricsSummary)
	if !ok {
		t.Fatalf("summary output = %T, want MetricsSummary", output)
	}
	return summary
}

func waitForRequest(t *testing.T, requested <-chan struct{}) {
	t.Helper()
	waitForRequests(t, requested, 1)
}

func waitForRequests(t *testing.T, requested <-chan struct{}, n int) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for i := 0; i < n; i++ {
		select {
		case <-requested:
		case <-timer.C:
			t.Fatalf("timed out waiting for request %d/%d", i+1, n)
		}
	}
}

func waitForFatal(t *testing.T, done <-chan error) error {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err, ok := <-done:
		if !ok {
			t.Fatalf("Done closed without fatal error")
		}
		if err == nil {
			t.Fatalf("Done yielded nil, want fatal error")
		}
		return err
	case <-timer.C:
		t.Fatalf("timed out waiting for fatal error")
		return nil
	}
}

func assertNoFatal(t *testing.T, done <-chan error, d time.Duration) {
	t.Helper()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case err := <-done:
		t.Fatalf("Done yielded %v, want worker to keep running", err)
	case <-timer.C:
	}
}

func waitForCounter(t *testing.T, env *contract.TestRunEnv, name string, want float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := env.CounterValue(name); got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("counter %q = %v, want at least %v", name, env.CounterValue(name), want)
}
