package kernel_test

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/dsl"
	"github.com/WuKongIM/wkbench/benchkit/kernel"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

func TestEngineAutoWiresUniqueMatchingPortsAndRunsInDependencyOrder(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(sourceUnit{calls: &calls})
	reg.MustRegister(sinkUnit{calls: &calls})

	engine := kernel.New(reg)
	result, err := engine.Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "auto-wire"},
		Units: map[string]dsl.UnitNode{
			"source": {Use: "test.source"},
			"sink":   {Use: "test.sink"},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if result.Status != kernel.StatusCompleted {
		t.Fatalf("unexpected status %s", result.Status)
	}
	if len(calls) != 2 || calls[0] != "source" || calls[1] != "sink:source-value" {
		t.Fatalf("unexpected run order/output: %#v", calls)
	}
}

func TestEngineRejectsAmbiguousAutoWire(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(sourceUnit{kind: "test.source_a/v1", calls: &calls})
	reg.MustRegister(sourceUnit{kind: "test.source_b/v1", calls: &calls})
	reg.MustRegister(sinkUnit{calls: &calls})

	engine := kernel.New(reg)
	err := engine.Validate(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "ambiguous"},
		Units: map[string]dsl.UnitNode{
			"a":    {Use: "test.source_a"},
			"b":    {Use: "test.source_b"},
			"sink": {Use: "test.sink"},
		},
	})
	if err == nil {
		t.Fatal("expected ambiguous auto-wire error")
	}
}

func TestEngineExplainShowsOrderContractsAndWiring(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(sourceUnit{calls: &calls})
	reg.MustRegister(sinkUnit{calls: &calls})

	explanation, err := kernel.New(reg).Explain(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "explain"},
		Units: map[string]dsl.UnitNode{
			"source": {Use: "test.source"},
			"sink":   {Use: "test.sink"},
		},
	})
	if err != nil {
		t.Fatalf("explain scenario: %v", err)
	}
	if explanation.RunID != "explain" {
		t.Fatalf("unexpected run id %q", explanation.RunID)
	}
	if fmt.Sprint(explanation.Order) != "[source sink]" {
		t.Fatalf("unexpected order: %#v", explanation.Order)
	}
	source := explanation.Units["source"]
	if source.Kind != "test.source/v1" {
		t.Fatalf("unexpected source kind %q", source.Kind)
	}
	if len(source.Outputs) != 1 || source.Outputs[0].Name != "value" || source.Outputs[0].Type != testValuePort {
		t.Fatalf("unexpected source outputs: %#v", source.Outputs)
	}
	sink := explanation.Units["sink"]
	if sink.Kind != "test.sink/v1" {
		t.Fatalf("unexpected sink kind %q", sink.Kind)
	}
	if len(sink.Inputs) != 1 || sink.Inputs[0].Name != "input" || sink.Inputs[0].Type != testValuePort {
		t.Fatalf("unexpected sink inputs: %#v", sink.Inputs)
	}
	if len(explanation.Wiring) != 1 {
		t.Fatalf("unexpected wiring: %#v", explanation.Wiring)
	}
	binding := explanation.Wiring[0]
	if binding.Unit != "sink" || binding.Input != "input" || binding.SourceUnit != "source" || binding.SourceOutput != "value" || binding.Type != testValuePort {
		t.Fatalf("unexpected binding: %#v", binding)
	}
}

func TestEngineExplainValidatesWithoutPlanningOrRunning(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(lifecycleProbeUnit{calls: &calls})

	_, err := kernel.New(reg).Explain(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "lifecycle"},
		Units: map[string]dsl.UnitNode{
			"probe": {Use: "test.lifecycle_probe"},
		},
	})
	if err != nil {
		t.Fatalf("explain scenario: %v", err)
	}
	want := []string{"validate:probe"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("unexpected lifecycle calls got %v want %v", calls, want)
	}
}

func TestEngineRecordsReportableOutputs(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(reportableSourceUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "reportable"},
		Units: map[string]dsl.UnitNode{
			"source": {Use: "test.reportable_source"},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	output := result.Units["source"].Outputs["value"]
	if output.Type != testValuePort {
		t.Fatalf("unexpected output type %s", output.Type)
	}
	if output.Value != "visible-value" {
		t.Fatalf("unexpected output value %#v", output.Value)
	}
}

func TestEngineRecordsEmittedMetrics(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(metricsUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "metrics"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.metrics"},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	metrics := result.Units["metrics"].Metrics
	counter := metrics["attempt_total"]
	if counter.Type != "counter" || counter.Count != 2 || counter.Sum != 3 {
		t.Fatalf("unexpected counter metric: %#v", counter)
	}
	duration := metrics["latency"]
	if duration.Type != "duration" || duration.Count != 2 ||
		math.Abs(duration.Sum-0.003) > 0.0000001 ||
		math.Abs(duration.Min-0.001) > 0.0000001 ||
		math.Abs(duration.Max-0.002) > 0.0000001 {
		t.Fatalf("unexpected duration metric: %#v", duration)
	}
}

func TestEnginePreservesMetricsWhenUnitRunFails(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(failingMetricUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "metrics-fail"},
		Units: map[string]dsl.UnitNode{
			"fail": {Use: "test.failing_metric"},
		},
	})
	if err == nil {
		t.Fatal("expected run error")
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	metric := result.Units["fail"].Metrics["before_fail_total"]
	if metric.Type != "counter" || metric.Count != 1 || metric.Sum != 1 {
		t.Fatalf("unexpected failed-unit metric: %#v", metric)
	}
}

func TestEngineClosesOutputsInReverseExecutionOrder(t *testing.T) {
	reg := registry.New()
	var events []string
	reg.MustRegister(closeableUnit{kind: "test.closeable_a/v1", outputName: "value", events: &events})
	reg.MustRegister(closeableUnit{kind: "test.closeable_b/v1", outputName: "value", events: &events})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "cleanup"},
		Units: map[string]dsl.UnitNode{
			"a": {Use: "test.closeable_a"},
			"b": {Use: "test.closeable_b", After: []string{"a"}},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if result.Status != kernel.StatusCompleted {
		t.Fatalf("unexpected status %s", result.Status)
	}
	want := []string{"run:a", "run:b", "close:b.value", "close:a.value"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("unexpected events got %v want %v", events, want)
	}
}

func TestEngineRecordsCleanupErrorsWithoutFailingRun(t *testing.T) {
	reg := registry.New()
	var events []string
	reg.MustRegister(closeableUnit{kind: "test.closeable/v1", outputName: "value", events: &events, closeErr: fmt.Errorf("close failed")})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "cleanup-error"},
		Units: map[string]dsl.UnitNode{
			"resource": {Use: "test.closeable"},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if result.Status != kernel.StatusCompleted {
		t.Fatalf("cleanup errors must not change status, got %s", result.Status)
	}
	cleanup := result.Units["resource"].Cleanup
	if len(cleanup) != 1 || cleanup[0].Output != "value" || cleanup[0].Error != "close failed" {
		t.Fatalf("unexpected cleanup results: %#v", cleanup)
	}
}

func TestEngineClosesExecutedOutputsWhenLaterUnitFails(t *testing.T) {
	reg := registry.New()
	var events []string
	reg.MustRegister(closeableUnit{kind: "test.closeable/v1", outputName: "value", events: &events})
	reg.MustRegister(failingRunUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "cleanup-on-fail"},
		Units: map[string]dsl.UnitNode{
			"resource": {Use: "test.closeable"},
			"fail":     {Use: "test.failing_run", After: []string{"resource"}},
		},
	})
	if err == nil {
		t.Fatal("expected run error")
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	want := []string{"run:resource", "close:resource.value"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("unexpected events got %v want %v", events, want)
	}
}

const testValuePort = contract.PortType("port.test.value/v1")

type sourceUnit struct {
	kind  string
	calls *[]string
}

func (u sourceUnit) Definition() contract.Definition {
	kind := u.kind
	if kind == "" {
		kind = "test.source/v1"
	}
	return contract.Definition{
		Kind: kind,
		Outputs: []contract.PortDef{
			{Name: "value", Type: testValuePort},
		},
	}
}

func (u sourceUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (u sourceUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (u sourceUnit) Run(ctx context.Context, env contract.RunEnv) error {
	*u.calls = append(*u.calls, env.UnitName())
	return env.SetOutput("value", "source-value")
}

type reportableSourceUnit struct{}

func (reportableSourceUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.reportable_source/v1",
		Outputs: []contract.PortDef{
			{Name: "value", Type: testValuePort},
		},
	}
}

func (reportableSourceUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (reportableSourceUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (reportableSourceUnit) Run(ctx context.Context, env contract.RunEnv) error {
	return env.SetOutput("value", reportableValue("visible-value"))
}

type reportableValue string

func (v reportableValue) ReportOutput() any { return string(v) }

type metricsUnit struct{}

func (metricsUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.metrics/v1",
		Metrics: []contract.MetricDef{
			{Name: "attempt_total", Type: "counter"},
			{Name: "latency", Type: "duration"},
		},
	}
}

func (metricsUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (metricsUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (metricsUnit) Run(ctx context.Context, env contract.RunEnv) error {
	env.EmitCounter("attempt_total", 1, nil)
	env.EmitCounter("attempt_total", 2, nil)
	env.ObserveDuration("latency", time.Millisecond, nil)
	env.ObserveDuration("latency", 2*time.Millisecond, nil)
	return nil
}

type failingMetricUnit struct{}

func (failingMetricUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.failing_metric/v1",
		Metrics: []contract.MetricDef{
			{Name: "before_fail_total", Type: "counter"},
		},
	}
}

func (failingMetricUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (failingMetricUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (failingMetricUnit) Run(ctx context.Context, env contract.RunEnv) error {
	env.EmitCounter("before_fail_total", 1, nil)
	return fmt.Errorf("boom")
}

type closeableUnit struct {
	kind       string
	outputName string
	events     *[]string
	closeErr   error
}

func (u closeableUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: u.kind,
		Outputs: []contract.PortDef{
			{Name: u.outputName, Type: testValuePort},
		},
	}
}

func (closeableUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (closeableUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (u closeableUnit) Run(ctx context.Context, env contract.RunEnv) error {
	*u.events = append(*u.events, "run:"+env.UnitName())
	return env.SetOutput(u.outputName, closeableValue{label: env.UnitName() + "." + u.outputName, events: u.events, err: u.closeErr})
}

type closeableValue struct {
	label  string
	events *[]string
	err    error
}

func (v closeableValue) Close() error {
	*v.events = append(*v.events, "close:"+v.label)
	return v.err
}

type failingRunUnit struct{}

func (failingRunUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.failing_run/v1"}
}

func (failingRunUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (failingRunUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (failingRunUnit) Run(context.Context, contract.RunEnv) error {
	return fmt.Errorf("boom")
}

type sinkUnit struct {
	calls *[]string
}

func (u sinkUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.sink/v1",
		Inputs: []contract.PortDef{
			{Name: "input", Type: testValuePort},
		},
	}
}

func (u sinkUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (u sinkUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (u sinkUnit) Run(ctx context.Context, env contract.RunEnv) error {
	value, err := contract.Input[string](env, "input")
	if err != nil {
		return err
	}
	*u.calls = append(*u.calls, env.UnitName()+":"+value)
	return nil
}

type lifecycleProbeUnit struct {
	calls *[]string
}

func (lifecycleProbeUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.lifecycle_probe/v1"}
}

func (u lifecycleProbeUnit) Validate(_ context.Context, env contract.ValidateEnv) error {
	*u.calls = append(*u.calls, "validate:"+env.UnitName())
	return nil
}

func (u lifecycleProbeUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	*u.calls = append(*u.calls, "plan")
	return contract.Plan{}, fmt.Errorf("plan must not run during explain")
}

func (u lifecycleProbeUnit) Run(context.Context, contract.RunEnv) error {
	*u.calls = append(*u.calls, "run")
	return fmt.Errorf("run must not run during explain")
}
