package metrics_collector

import (
	"context"
	"encoding/json"
	"strings"
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
`), metricFilter{})
	if parseErrors != 7 {
		t.Fatalf("parse errors = %d, want 7", parseErrors)
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

func newEnv(spec map[string]any) *contract.TestRunEnv {
	return contract.NewTestRunEnv("run-1", "collector", nil, spec)
}
