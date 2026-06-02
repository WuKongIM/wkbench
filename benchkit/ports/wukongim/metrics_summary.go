// Package wukongim defines WuKongIM-specific result ports.
package wukongim

import "github.com/WuKongIM/wkbench/benchkit/contract"

// MetricsSummaryV1 is the port type for WuKongIM metrics summaries.
const MetricsSummaryV1 contract.PortType = "port.wukongim.metrics_summary/v1"

// MetricsSummary is a compact WuKongIM metrics scrape summary consumed by report units.
type MetricsSummary struct {
	ScrapeTicks        int64                 `json:"scrape_ticks"`
	SelectedSamples    int64                 `json:"selected_samples"`
	DroppedMetricNames int64                 `json:"dropped_metric_names"`
	Nodes              []NodeScrapeSummary   `json:"nodes"`
	LatencyP95MS       float64               `json:"latency_p95_ms"`
	LatencyP99MS       float64               `json:"latency_p99_ms"`
	Latest             []MetricSampleSummary `json:"latest"`
}

// NodeScrapeSummary records scrape outcomes for one WuKongIM node.
type NodeScrapeSummary struct {
	Address string `json:"address"`
	Success int64  `json:"success"`
	Errors  int64  `json:"errors"`
}

// MetricSampleSummary records one latest selected metric sample.
type MetricSampleSummary struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

// ReportOutput implements contract.ReportableOutput.
func (s MetricsSummary) ReportOutput() any {
	return map[string]any{
		"scrape_ticks":         s.ScrapeTicks,
		"selected_samples":     s.SelectedSamples,
		"dropped_metric_names": s.DroppedMetricNames,
		"nodes":                s.Nodes,
		"latency_p95_ms":       s.LatencyP95MS,
		"latency_p99_ms":       s.LatencyP99MS,
		"latest":               s.Latest,
	}
}
