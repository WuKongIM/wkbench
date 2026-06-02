package wukongim

import (
	"reflect"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestMetricsSummaryPortContract(t *testing.T) {
	if got, want := MetricsSummaryV1, contract.PortType("port.wukongim.metrics_summary/v1"); got != want {
		t.Fatalf("MetricsSummaryV1 = %q, want %q", got, want)
	}

	summary := MetricsSummary{
		ScrapeTicks:        12,
		SelectedSamples:    34,
		DroppedMetricNames: 2,
		Nodes: []NodeScrapeSummary{
			{Address: "127.0.0.1:5300", Success: 10, Errors: 1},
			{Address: "127.0.0.2:5300", Success: 9, Errors: 3},
		},
		LatencyP95MS: 6.78,
		LatencyP99MS: 9.01,
		Latest: []MetricSampleSummary{
			{
				Name:   "wukongim_channel_count",
				Labels: map[string]string{"node": "node-a"},
				Value:  42,
			},
		},
	}

	output, ok := summary.ReportOutput().(map[string]any)
	if !ok {
		t.Fatalf("ReportOutput() type = %T, want map[string]any", summary.ReportOutput())
	}

	assertMapValue(t, output, "scrape_ticks", summary.ScrapeTicks)
	assertMapValue(t, output, "selected_samples", summary.SelectedSamples)
	assertMapValue(t, output, "dropped_metric_names", summary.DroppedMetricNames)
	assertMapValue(t, output, "nodes", summary.Nodes)
	assertMapValue(t, output, "latency_p95_ms", summary.LatencyP95MS)
	assertMapValue(t, output, "latency_p99_ms", summary.LatencyP99MS)
	assertMapValue(t, output, "latest", summary.Latest)
}

func TestMetricsSummaryReportOutputKeepsZeroValueKeys(t *testing.T) {
	output := MetricsSummary{}.ReportOutput().(map[string]any)

	for _, key := range []string{
		"scrape_ticks",
		"selected_samples",
		"dropped_metric_names",
		"nodes",
		"latency_p95_ms",
		"latency_p99_ms",
		"latest",
	} {
		if _, ok := output[key]; !ok {
			t.Fatalf("ReportOutput() missing zero-value key %q: %#v", key, output)
		}
	}
}

func assertMapValue(t *testing.T, values map[string]any, key string, want any) {
	t.Helper()

	if got := values[key]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ReportOutput()[%q] = %#v, want %#v", key, got, want)
	}
}
