package kernel_test

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
	_ "unsafe"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/dsl"
	"github.com/WuKongIM/wkbench/benchkit/kernel"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

//go:linkname timelineFields github.com/WuKongIM/wkbench/benchkit/kernel.timelineFields
func timelineFields(start, end time.Time) (string, string, int64)

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

func TestEnginePlanShowsOrderUnitPlansAndWiring(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(planningSourceUnit{calls: &calls})
	reg.MustRegister(planningSinkUnit{calls: &calls})

	result, err := kernel.New(reg).Plan(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "plan"},
		Units: map[string]dsl.UnitNode{
			"source": {Use: "test.planning_source"},
			"sink":   {Use: "test.planning_sink"},
		},
	})
	if err != nil {
		t.Fatalf("plan scenario: %v", err)
	}
	if result.RunID != "plan" || result.Status != kernel.StatusCompleted {
		t.Fatalf("unexpected result: %#v", result)
	}
	if fmt.Sprint(result.Order) != "[source sink]" {
		t.Fatalf("unexpected order: %#v", result.Order)
	}
	if result.Units["source"].Kind != "test.planning_source/v1" {
		t.Fatalf("unexpected source result: %#v", result.Units["source"])
	}
	if result.Units["source"].Plan.UnitName != "source" || len(result.Units["source"].Plan.Shards) != 1 {
		t.Fatalf("unexpected source plan: %#v", result.Units["source"].Plan)
	}
	if len(result.Wiring) != 1 || result.Wiring[0].Unit != "sink" || result.Wiring[0].SourceUnit != "source" {
		t.Fatalf("unexpected wiring: %#v", result.Wiring)
	}
	want := []string{"validate:source", "plan:source", "validate:sink", "plan:sink"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("unexpected calls got %v want %v", calls, want)
	}
}

func TestEnginePlanDoesNotRunUnits(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(planningLifecycleUnit{calls: &calls})

	result, err := kernel.New(reg).Plan(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "lifecycle"},
		Units: map[string]dsl.UnitNode{
			"probe": {Use: "test.planning_lifecycle"},
		},
	})
	if err != nil {
		t.Fatalf("plan scenario: %v", err)
	}
	if result.Status != kernel.StatusCompleted {
		t.Fatalf("unexpected status %s", result.Status)
	}
	want := []string{"validate:probe", "plan"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("unexpected lifecycle calls got %v want %v", calls, want)
	}
}

func TestEnginePlanRecordsPlanFailure(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(failingPlanUnit{})

	result, err := kernel.New(reg).Plan(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "plan-fail"},
		Units: map[string]dsl.UnitNode{
			"fail": {Use: "test.failing_plan"},
		},
	})
	if err == nil {
		t.Fatal("expected plan error")
	}
	if result.Status != kernel.StatusPlanFailed {
		t.Fatalf("unexpected result status %s", result.Status)
	}
	unit := result.Units["fail"]
	if unit.Status != kernel.StatusPlanFailed || unit.Error != "boom" {
		t.Fatalf("unexpected failed unit: %#v", unit)
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
	declaredCounter := metrics["not_emitted_total"]
	if declaredCounter.Type != "counter" || declaredCounter.Count != 0 || declaredCounter.Sum != 0 {
		t.Fatalf("unexpected declared counter metric: %#v", declaredCounter)
	}
	labelledA := metrics["labelled_total{a=b%2Cc%3Dd}"]
	if labelledA.Type != "counter" || labelledA.Count != 1 || labelledA.Sum != 1 || labelledA.Labels["a"] != "b,c=d" {
		t.Fatalf("unexpected first labelled metric: %#v", labelledA)
	}
	labelledB := metrics["labelled_total{a=b,c=d}"]
	if labelledB.Type != "counter" || labelledB.Count != 1 || labelledB.Sum != 2 || labelledB.Labels["a"] != "b" || labelledB.Labels["c"] != "d" {
		t.Fatalf("unexpected second labelled metric: %#v", labelledB)
	}
	duration := metrics["latency"]
	if duration.Type != "duration" || duration.Count != 2 ||
		math.Abs(duration.Sum-0.003) > 0.0000001 ||
		math.Abs(duration.Min-0.001) > 0.0000001 ||
		math.Abs(duration.Max-0.002) > 0.0000001 ||
		math.Abs(duration.P95-0.002) > 0.0000001 ||
		math.Abs(duration.P99-0.002) > 0.0000001 {
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

func TestEngineRunsBackgroundUnitDuringForegroundWork(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events})
	reg.MustRegister(foregroundProbeUnit{events: events})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
			"traffic": {Use: "test.foreground_probe/v1", After: []string{"metrics"}},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	got := drainEvents(events)
	want := []string{"metrics:start", "traffic:run", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
	unit := result.Units["metrics"]
	if unit.Status != kernel.StatusCompleted {
		t.Fatalf("background status = %s", unit.Status)
	}
	summary, ok := unit.Outputs["summary"].Value.(map[string]any)
	if !ok || summary["stopped"] != true {
		t.Fatalf("background output was not snapshotted after Stop: %#v", unit.Outputs)
	}
	if unit.Metrics["ticks_total"].Sum != 1 {
		t.Fatalf("background metrics missing post-foreground emission: %#v", unit.Metrics)
	}
	assertUnitTimeline(t, unit)
}

func TestEngineStopsBackgroundUnitsInReverseStartOrder(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_a/v1", title: "a", events: events})
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_b/v1", title: "b", events: events})
	reg.MustRegister(foregroundProbeUnit{events: events})

	_, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "reverse"},
		Units: map[string]dsl.UnitNode{
			"a":       {Use: "test.bg_a/v1"},
			"b":       {Use: "test.bg_b/v1", After: []string{"a"}},
			"traffic": {Use: "test.foreground_probe/v1", After: []string{"b"}},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	got := drainEvents(events)
	want := []string{"a:start", "b:start", "traffic:run", "b:stop", "a:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineTreatsNilBackgroundTaskAsStartFailure(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_active/v1", events: events})
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_nil/v1", events: events, nilTask: true})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "nil-background-task"},
		Units: map[string]dsl.UnitNode{
			"active": {Use: "test.bg_active/v1"},
			"nil":    {Use: "test.bg_nil/v1", After: []string{"active"}},
		},
	})
	if err == nil {
		t.Fatal("expected nil task start error")
	}
	if !strings.Contains(err.Error(), `unit "nil" start`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	failed := result.Units["nil"]
	if failed.Status != kernel.StatusWorkerFailed || failed.Error != "background task is nil" {
		t.Fatalf("unexpected failed unit: %#v", failed)
	}
	assertUnitTimeline(t, failed)
	assertBackgroundStopped(t, result.Units["active"])
	got := drainEvents(events)
	want := []string{"active:start", "nil:start", "active:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineRecordsBackgroundStartErrorAndStopsActiveBackgrounds(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_active/v1", events: events})
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_failing_start/v1", events: events, startErr: fmt.Errorf("start failed")})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background-start-fail"},
		Units: map[string]dsl.UnitNode{
			"active": {Use: "test.bg_active/v1"},
			"fail":   {Use: "test.bg_failing_start/v1", After: []string{"active"}},
		},
	})
	if err == nil {
		t.Fatal("expected start error")
	}
	if !strings.Contains(err.Error(), `unit "fail" start`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	failed := result.Units["fail"]
	if failed.Status != kernel.StatusWorkerFailed || failed.Error != "start failed" {
		t.Fatalf("unexpected failed unit: %#v", failed)
	}
	assertUnitTimeline(t, failed)
	assertBackgroundStopped(t, result.Units["active"])
	got := drainEvents(events)
	want := []string{"active:start", "fail:start", "active:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineRecordsBackgroundStopError(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events, stopErr: fmt.Errorf("stop failed")})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background-stop-fail"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
		},
	})
	if err == nil {
		t.Fatal("expected stop error")
	}
	if !strings.Contains(err.Error(), `unit "metrics" stop`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	unit := result.Units["metrics"]
	if unit.Status != kernel.StatusWorkerFailed || unit.Error != "stop failed" {
		t.Fatalf("unexpected background unit: %#v", unit)
	}
	assertUnitTimeline(t, unit)
	if unit.Metrics["ticks_total"].Sum != 1 {
		t.Fatalf("background metrics missing stop emission: %#v", unit.Metrics)
	}
	got := drainEvents(events)
	want := []string{"metrics:start", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineCancelsForegroundWhenBackgroundFails(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events, doneErr: fmt.Errorf("scrape failed"), doneDelay: 10 * time.Millisecond})
	reg.MustRegister(cancelAwareForegroundUnit{})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := kernel.New(reg).Run(ctx, dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background-fatal-cancels-foreground"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
			"traffic": {Use: "test.cancel_aware_foreground/v1", After: []string{"metrics"}},
		},
	})
	if err == nil {
		t.Fatal("expected background fatal error")
	}
	errorText := err.Error()
	if !strings.Contains(errorText, `unit "metrics" background`) || !strings.Contains(errorText, "scrape failed") {
		t.Fatalf("error = %q, want background fatal error", errorText)
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	background := result.Units["metrics"]
	if background.Status != kernel.StatusWorkerFailed || !strings.Contains(background.Error, "scrape failed") {
		t.Fatalf("unexpected background unit: %#v", background)
	}
	summary, ok := background.Outputs["summary"].Value.(map[string]any)
	if !ok || summary["stopped"] != true {
		t.Fatalf("background output was not snapshotted after Stop: %#v", background.Outputs)
	}
	if background.Metrics["ticks_total"].Sum != 1 {
		t.Fatalf("background metrics missing stop emission: %#v", background.Metrics)
	}
	assertUnitTimeline(t, background)
	foreground := result.Units["traffic"]
	if foreground.Status != kernel.StatusWorkerFailed || !strings.Contains(foreground.Error, "context canceled") {
		t.Fatalf("unexpected foreground unit: %#v", foreground)
	}
	assertUnitTimeline(t, foreground)
	got := drainEvents(events)
	want := []string{"metrics:start", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineRecordsBackgroundDoneFatalDuringShutdown(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events, doneErr: fmt.Errorf("scrape failed"), doneErrOnStop: true})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background-fatal-during-shutdown"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
		},
	})
	if err == nil {
		t.Fatal("expected background fatal error")
	}
	if !strings.Contains(err.Error(), `unit "metrics" background`) || !strings.Contains(err.Error(), "scrape failed") {
		t.Fatalf("error = %q, want background fatal error", err.Error())
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	background := result.Units["metrics"]
	if background.Status != kernel.StatusWorkerFailed || !strings.Contains(background.Error, "scrape failed") {
		t.Fatalf("unexpected background unit: %#v", background)
	}
	summary, ok := background.Outputs["summary"].Value.(map[string]any)
	if !ok || summary["stopped"] != true {
		t.Fatalf("background output was not snapshotted after Stop: %#v", background.Outputs)
	}
	if background.Metrics["ticks_total"].Sum != 1 {
		t.Fatalf("background metrics missing stop emission: %#v", background.Metrics)
	}
	assertUnitTimeline(t, background)
	got := drainEvents(events)
	want := []string{"metrics:start", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineDoesNotHangWhenBackgroundDoneStaysOpenAfterStop(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events, leaveDoneOpen: true})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct {
		result kernel.Result
		err    error
	}, 1)
	go func() {
		result, err := kernel.New(reg).Run(ctx, dsl.Scenario{
			Version: "wkbench/v2",
			Run:     dsl.RunConfig{ID: "background-done-left-open"},
			Units: map[string]dsl.UnitNode{
				"metrics": {Use: "test.background_probe/v1"},
			},
		})
		done <- struct {
			result kernel.Result
			err    error
		}{result: result, err: err}
	}()

	select {
	case out := <-done:
		if out.err == nil {
			t.Fatal("expected background shutdown error")
		}
		if !strings.Contains(out.err.Error(), `unit "metrics" background`) || !strings.Contains(out.err.Error(), "done did not complete") {
			t.Fatalf("error = %q, want bounded done wait error", out.err.Error())
		}
		if out.result.Status != kernel.StatusWorkerFailed {
			t.Fatalf("unexpected status %s", out.result.Status)
		}
		background := out.result.Units["metrics"]
		if background.Status != kernel.StatusWorkerFailed || !strings.Contains(background.Error, "done did not complete") {
			t.Fatalf("unexpected background unit: %#v", background)
		}
		assertUnitTimeline(t, background)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("engine hung waiting for background Done after Stop")
	}
}

func TestEngineReportsBackgroundDoneFatalAndStopErrorDuringShutdown(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{
		events:        events,
		doneErr:       fmt.Errorf("scrape failed"),
		doneErrOnStop: true,
		stopErr:       fmt.Errorf("stop failed"),
	})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background-done-fatal-and-stop-error"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
		},
	})
	if err == nil {
		t.Fatal("expected background fatal and stop errors")
	}
	errorText := err.Error()
	if !strings.Contains(errorText, `unit "metrics" background`) || !strings.Contains(errorText, "scrape failed") ||
		!strings.Contains(errorText, `unit "metrics" stop`) || !strings.Contains(errorText, "stop failed") {
		t.Fatalf("error = %q, want background fatal and stop errors", errorText)
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	background := result.Units["metrics"]
	if background.Status != kernel.StatusWorkerFailed ||
		!strings.Contains(background.Error, "scrape failed") ||
		!strings.Contains(background.Error, "stop failed") {
		t.Fatalf("unexpected background unit: %#v", background)
	}
	assertUnitTimeline(t, background)
}

func TestEngineStopsBackgroundWhenForegroundFails(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events})
	reg.MustRegister(failingRunUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "foreground-run-fail"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
			"fail":    {Use: "test.failing_run/v1", After: []string{"metrics"}},
		},
	})
	if err == nil {
		t.Fatal("expected foreground run error")
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	failed := result.Units["fail"]
	if failed.Status != kernel.StatusWorkerFailed || failed.Error != "boom" {
		t.Fatalf("unexpected failed foreground unit: %#v", failed)
	}
	background := result.Units["metrics"]
	assertBackgroundStopped(t, background)
	got := drainEvents(events)
	want := []string{"metrics:start", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineReportsBackgroundStopErrorWhenForegroundRunFails(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events, stopErr: fmt.Errorf("stop failed")})
	reg.MustRegister(failingRunUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "foreground-run-fail-background-stop-fail"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
			"fail":    {Use: "test.failing_run/v1", After: []string{"metrics"}},
		},
	})
	if err == nil {
		t.Fatal("expected foreground and background stop error")
	}
	errorText := err.Error()
	if !strings.Contains(errorText, `unit "fail" run`) || !strings.Contains(errorText, "boom") ||
		!strings.Contains(errorText, `unit "metrics" stop`) || !strings.Contains(errorText, "stop failed") {
		t.Fatalf("error = %q, want foreground run and background stop errors", errorText)
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	failed := result.Units["fail"]
	if failed.Status != kernel.StatusWorkerFailed || failed.Error != "boom" {
		t.Fatalf("unexpected failed foreground unit: %#v", failed)
	}
	background := result.Units["metrics"]
	if background.Status != kernel.StatusWorkerFailed || background.Error != "stop failed" {
		t.Fatalf("unexpected background unit: %#v", background)
	}
	got := drainEvents(events)
	want := []string{"metrics:start", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestEngineStopsBackgroundWhenLaterPlanFails(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events})
	reg.MustRegister(failingPlanUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "plan-fail-after-background"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
			"fail":    {Use: "test.failing_plan/v1", After: []string{"metrics"}},
		},
	})
	if err == nil {
		t.Fatal("expected plan error")
	}
	if result.Status != kernel.StatusPlanFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	failed := result.Units["fail"]
	if failed.Status != kernel.StatusPlanFailed || failed.Error != "boom" {
		t.Fatalf("unexpected failed plan unit: %#v", failed)
	}
	assertBackgroundStopped(t, result.Units["metrics"])
	got := drainEvents(events)
	want := []string{"metrics:start", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
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

func TestEngineRecordsUnitTimeline(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(timelineUnit{})
	scenario := dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "timeline"},
		Units: map[string]dsl.UnitNode{
			"work": {Use: "test.timeline/v1"},
		},
	}

	result, err := kernel.New(reg).Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	unit := result.Units["work"]
	assertUnitTimeline(t, unit)
}

func TestEngineRecordsTimelineWhenUnitRunFails(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(failingTimelineUnit{})

	result, err := kernel.New(reg).Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "timeline-fail"},
		Units: map[string]dsl.UnitNode{
			"fail": {Use: "test.failing_timeline/v1"},
		},
	})
	if err == nil {
		t.Fatal("expected run error")
	}
	if result.Status != kernel.StatusWorkerFailed {
		t.Fatalf("unexpected status %s", result.Status)
	}
	unit := result.Units["fail"]
	if unit.Status != kernel.StatusWorkerFailed || unit.Error != "boom" {
		t.Fatalf("unexpected failed unit: %#v", unit)
	}
	assertUnitTimeline(t, unit)
}

func TestTimelineFieldsClampsPositiveSubmillisecondElapsed(t *testing.T) {
	start := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	end := start.Add(500 * time.Microsecond)

	_, _, elapsedMS := timelineFields(start, end)
	if elapsedMS != 1 {
		t.Fatalf("ElapsedMS = %d, want 1", elapsedMS)
	}
}

func assertUnitTimeline(t *testing.T, unit kernel.UnitResult) {
	t.Helper()
	if unit.StartedAt == "" {
		t.Fatalf("StartedAt is empty")
	}
	if unit.EndedAt == "" {
		t.Fatalf("EndedAt is empty")
	}
	started, err := time.Parse(time.RFC3339Nano, unit.StartedAt)
	if err != nil {
		t.Fatalf("StartedAt parse: %v", err)
	}
	ended, err := time.Parse(time.RFC3339Nano, unit.EndedAt)
	if err != nil {
		t.Fatalf("EndedAt parse: %v", err)
	}
	if ended.Before(started) {
		t.Fatalf("EndedAt %q is before StartedAt %q", unit.EndedAt, unit.StartedAt)
	}
	if !strings.HasSuffix(unit.StartedAt, "Z") {
		t.Fatalf("StartedAt = %q, want UTC timestamp ending in Z", unit.StartedAt)
	}
	if !strings.HasSuffix(unit.EndedAt, "Z") {
		t.Fatalf("EndedAt = %q, want UTC timestamp ending in Z", unit.EndedAt)
	}
	if unit.ElapsedMS <= 0 {
		t.Fatalf("ElapsedMS = %d, want positive", unit.ElapsedMS)
	}
}

func assertBackgroundStopped(t *testing.T, unit kernel.UnitResult) {
	t.Helper()
	if unit.Status != kernel.StatusCompleted {
		t.Fatalf("background status = %s", unit.Status)
	}
	summary, ok := unit.Outputs["summary"].Value.(map[string]any)
	if !ok || summary["stopped"] != true {
		t.Fatalf("background output was not snapshotted after Stop: %#v", unit.Outputs)
	}
	if unit.Metrics["ticks_total"].Sum != 1 {
		t.Fatalf("background metrics missing stop emission: %#v", unit.Metrics)
	}
	assertUnitTimeline(t, unit)
}

type timelineUnit struct{}

func (timelineUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.timeline/v1"}
}
func (timelineUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (timelineUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (timelineUnit) Run(context.Context, contract.RunEnv) error {
	time.Sleep(time.Millisecond)
	return nil
}

type failingTimelineUnit struct{}

func (failingTimelineUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.failing_timeline/v1"}
}
func (failingTimelineUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (failingTimelineUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (failingTimelineUnit) Run(context.Context, contract.RunEnv) error {
	time.Sleep(time.Millisecond)
	return fmt.Errorf("boom")
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

type planningSourceUnit struct {
	calls *[]string
}

func (u planningSourceUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.planning_source/v1",
		Outputs: []contract.PortDef{
			{Name: "value", Type: testValuePort},
		},
	}
}

func (u planningSourceUnit) Validate(_ context.Context, env contract.ValidateEnv) error {
	*u.calls = append(*u.calls, "validate:"+env.UnitName())
	return nil
}

func (u planningSourceUnit) Plan(_ context.Context, env contract.PlanEnv) (contract.Plan, error) {
	*u.calls = append(*u.calls, "plan:"+env.UnitName())
	return contract.Plan{
		UnitName: env.UnitName(),
		Shards: []any{
			map[string]any{"name": env.UnitName()},
		},
	}, nil
}

func (u planningSourceUnit) Run(context.Context, contract.RunEnv) error {
	*u.calls = append(*u.calls, "run")
	return fmt.Errorf("run must not execute during plan")
}

type planningSinkUnit struct {
	calls *[]string
}

func (u planningSinkUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.planning_sink/v1",
		Inputs: []contract.PortDef{
			{Name: "input", Type: testValuePort},
		},
	}
}

func (u planningSinkUnit) Validate(_ context.Context, env contract.ValidateEnv) error {
	*u.calls = append(*u.calls, "validate:"+env.UnitName())
	return nil
}

func (u planningSinkUnit) Plan(_ context.Context, env contract.PlanEnv) (contract.Plan, error) {
	*u.calls = append(*u.calls, "plan:"+env.UnitName())
	return contract.Plan{UnitName: env.UnitName()}, nil
}

func (u planningSinkUnit) Run(context.Context, contract.RunEnv) error {
	*u.calls = append(*u.calls, "run")
	return fmt.Errorf("run must not execute during plan")
}

type planningLifecycleUnit struct {
	calls *[]string
}

func (planningLifecycleUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.planning_lifecycle/v1"}
}

func (u planningLifecycleUnit) Validate(_ context.Context, env contract.ValidateEnv) error {
	*u.calls = append(*u.calls, "validate:"+env.UnitName())
	return nil
}

func (u planningLifecycleUnit) Plan(_ context.Context, env contract.PlanEnv) (contract.Plan, error) {
	*u.calls = append(*u.calls, "plan")
	return contract.Plan{UnitName: env.UnitName()}, nil
}

func (u planningLifecycleUnit) Run(context.Context, contract.RunEnv) error {
	*u.calls = append(*u.calls, "run")
	return fmt.Errorf("run must not execute during plan")
}

type failingPlanUnit struct{}

func (failingPlanUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.failing_plan/v1"}
}

func (failingPlanUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (failingPlanUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, fmt.Errorf("boom")
}

func (failingPlanUnit) Run(context.Context, contract.RunEnv) error {
	return nil
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
			{Name: "not_emitted_total", Type: "counter"},
			{Name: "labelled_total", Type: "counter"},
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
	labels := contract.Labels{"a": "b,c=d"}
	env.EmitCounter("labelled_total", 1, labels)
	labels["a"] = "mutated"
	env.EmitCounter("labelled_total", 2, contract.Labels{"a": "b", "c": "d"})
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

type backgroundProbeUnit struct {
	kind          string
	title         string
	events        chan string
	startErr      error
	nilTask       bool
	stopErr       error
	doneErr       error
	doneErrOnStop bool
	leaveDoneOpen bool
	doneDelay     time.Duration
}

func (u backgroundProbeUnit) Definition() contract.Definition {
	kind := u.kind
	if kind == "" {
		kind = "test.background_probe/v1"
	}
	return contract.Definition{
		Kind:    kind,
		Title:   u.title,
		Outputs: []contract.PortDef{{Name: "summary", Type: "port.test.summary/v1"}},
		Metrics: []contract.MetricDef{{Name: "ticks_total", Type: "counter"}},
	}
}

func (u backgroundProbeUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (u backgroundProbeUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (u backgroundProbeUnit) Run(context.Context, contract.RunEnv) error {
	return fmt.Errorf("background unit Run should not be called")
}
func (u backgroundProbeUnit) Start(_ context.Context, env contract.RunEnv) (contract.BackgroundTask, error) {
	name := env.UnitName()
	u.events <- name + ":start"
	if u.startErr != nil {
		return nil, u.startErr
	}
	if u.nilTask {
		return nil, nil
	}
	task := &backgroundProbeTask{
		name:          name,
		events:        u.events,
		env:           env,
		done:          make(chan error, 1),
		stopErr:       u.stopErr,
		doneErr:       u.doneErr,
		doneErrOnStop: u.doneErrOnStop,
		leaveDoneOpen: u.leaveDoneOpen,
	}
	if u.doneErr != nil && !u.doneErrOnStop {
		delay := u.doneDelay
		go func() {
			time.Sleep(delay)
			task.finishDone(u.doneErr)
		}()
	}
	return task, nil
}

type backgroundProbeTask struct {
	name          string
	events        chan string
	env           contract.RunEnv
	done          chan error
	doneMu        sync.Mutex
	closed        bool
	stopErr       error
	doneErr       error
	doneErrOnStop bool
	leaveDoneOpen bool
}

func (t *backgroundProbeTask) Done() <-chan error { return t.done }
func (t *backgroundProbeTask) Stop(context.Context) error {
	t.events <- t.name + ":stop"
	t.env.EmitCounter("ticks_total", 1, nil)
	if err := t.env.SetOutput("summary", backgroundProbeSummary{"stopped": true}); err != nil {
		return err
	}
	if t.doneErrOnStop {
		t.finishDone(t.doneErr)
		return t.stopErr
	}
	if t.leaveDoneOpen {
		return t.stopErr
	}
	t.closeDone()
	return t.stopErr
}

func (t *backgroundProbeTask) finishDone(err error) {
	t.doneMu.Lock()
	defer t.doneMu.Unlock()
	if t.closed {
		return
	}
	t.done <- err
	close(t.done)
	t.closed = true
}

func (t *backgroundProbeTask) closeDone() {
	t.doneMu.Lock()
	defer t.doneMu.Unlock()
	if t.closed {
		return
	}
	close(t.done)
	t.closed = true
}

type backgroundProbeSummary map[string]any

func (s backgroundProbeSummary) ReportOutput() any {
	return map[string]any(s)
}

type foregroundProbeUnit struct {
	events chan string
}

func (foregroundProbeUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.foreground_probe/v1"}
}
func (foregroundProbeUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (foregroundProbeUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (u foregroundProbeUnit) Run(_ context.Context, env contract.RunEnv) error {
	u.events <- env.UnitName() + ":run"
	return nil
}

type cancelAwareForegroundUnit struct{}

func (cancelAwareForegroundUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.cancel_aware_foreground/v1"}
}
func (cancelAwareForegroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (cancelAwareForegroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (cancelAwareForegroundUnit) Run(ctx context.Context, _ contract.RunEnv) error {
	<-ctx.Done()
	return ctx.Err()
}

func drainEvents(events chan string) []string {
	var out []string
	for {
		select {
		case event := <-events:
			out = append(out, event)
		default:
			return out
		}
	}
}
