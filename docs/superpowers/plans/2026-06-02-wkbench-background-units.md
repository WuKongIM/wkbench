# wkbench Background Units Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add first-class background units and a WuKongIM metrics collector that scrapes service metrics while foreground send workloads run.

**Architecture:** Extend the existing `contract.Unit` model with an optional `BackgroundUnit` lifecycle so current units keep their blocking `Run` behavior. The kernel starts background tasks in graph order, lets foreground units continue, stops background tasks in reverse order, then snapshots outputs, metrics, timing, cleanup, and artifacts. WuKongIM metrics samples are written to a bounded JSONL artifact with a compact reportable summary output.

**Tech Stack:** Go, wkbench contract/kernel/report packages, `net/http`, `httptest`, `time.Ticker`, `encoding/json`, `regexp`, existing scenario YAML and shell smoke script tests.

---

## File Structure

- Modify `benchkit/contract/types.go`: add `BackgroundUnit`, `BackgroundTask`, `RunEnv.OpenArtifact`, `TestRunEnv.OpenArtifact`, and optional artifact content type metadata.
- Modify `benchkit/contract/types_test.go`: cover `TestRunEnv` artifact behavior and compile-time background interfaces.
- Modify `benchkit/kernel/kernel.go`: record unit timelines, run background lifecycle, stop active tasks, collect background results after `Stop`, and record artifact metadata.
- Modify `benchkit/kernel/kernel_test.go`: add focused kernel tests for timelines, background ordering, failure propagation, cancellation, reverse stop order, metrics after foreground, output snapshotting, and artifact handling.
- Modify `benchkit/report/report.go`: render timing and artifact rows in `summary.md`; keep raw samples out of Markdown.
- Modify `benchkit/report/report_test.go`: assert artifact and timing rows are present and use millisecond formatting.
- Create `benchkit/ports/wukongim/metrics_summary.go`: public reportable `MetricsSummaryV1` port and compact structs for scrape and selected metric summaries.
- Create `benchkit/ports/wukongim/metrics_summary_test.go`: verify port type and report output shape.
- Create `units/wukongim/metrics_collector/unit.go`: unit definition, spec validation, plan, and `BackgroundUnit.Start`.
- Create `units/wukongim/metrics_collector/collector.go`: ticker worker, scrape concurrency, JSONL artifact writing, strict/non-strict error handling, final summary publication.
- Create `units/wukongim/metrics_collector/prometheus.go`: small Prometheus text parser and include/exclude metric filtering.
- Create `units/wukongim/metrics_collector/unit_test.go`: validation, parser, scrape success, non-strict errors, strict fatal errors, summary, and artifact tests.
- Modify `cmd/wkbench/main.go`: register `wukongim.metrics_collector/v1`.
- Modify `cmd/wkbench/main_test.go`: include collector in `list-units` and validate an example collector scenario.
- Create `examples/wukongim-send-rate-with-metrics.yaml`: runnable scenario showing target, metrics, identities, sessions, and send traffic wiring.
- Modify `scripts/bench-wukongim-three-node-send-rate-sweep.sh`: add `--collect-metrics`, interval, include, and exclude flags; render metrics unit per generated scenario.
- Modify `scripts/smoke_test.go`: dry-run assertions for collector rendering and dependency placement.

All commands below run from `/Users/tt/Desktop/work/go/WuKongIM-v2/wkbench` unless a task explicitly says otherwise.

---

### Task 1: Record Unit Timeline Fields

**Files:**
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`
- Modify: `benchkit/report/report.go`
- Modify: `benchkit/report/report_test.go`

- [ ] **Step 1: Write the failing kernel timeline test**

Append this test to `benchkit/kernel/kernel_test.go`:

```go
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

	result, err := New(reg).Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	unit := result.Units["work"]
	if unit.StartedAt == "" {
		t.Fatalf("StartedAt is empty")
	}
	if unit.EndedAt == "" {
		t.Fatalf("EndedAt is empty")
	}
	if unit.ElapsedMS <= 0 {
		t.Fatalf("ElapsedMS = %d, want positive", unit.ElapsedMS)
	}
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
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
GOWORK=off go test ./benchkit/kernel -run TestEngineRecordsUnitTimeline -count=1
```

Expected: FAIL because `UnitResult.StartedAt`, `EndedAt`, and `ElapsedMS` do not exist.

- [ ] **Step 3: Add timeline fields and set them for normal units**

In `benchkit/kernel/kernel.go`, extend `UnitResult`:

```go
	// StartedAt is the RFC3339Nano timestamp when runtime work started.
	StartedAt string `json:"started_at,omitempty"`
	// EndedAt is the RFC3339Nano timestamp when runtime work ended.
	EndedAt string `json:"ended_at,omitempty"`
	// ElapsedMS is the runtime elapsed wall time in milliseconds.
	ElapsedMS int64 `json:"elapsed_ms,omitempty"`
```

Add this helper near the other kernel helpers:

```go
func timelineFields(start, end time.Time) (string, string, int64) {
	elapsed := end.Sub(start).Milliseconds()
	if elapsed < 1 && end.After(start) {
		elapsed = 1
	}
	return start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), elapsed
}
```

Wrap the normal unit `Run` call:

```go
start := time.Now()
err := node.unit.Run(ctx, env)
end := time.Now()
startedAt, endedAt, elapsedMS := timelineFields(start, end)
if err != nil {
	result.Status = StatusWorkerFailed
	result.Units[name] = UnitResult{
		Kind:      node.def.Kind,
		Status:    StatusWorkerFailed,
		Error:     err.Error(),
		Metrics:   env.metrics.results(),
		StartedAt: startedAt,
		EndedAt:   endedAt,
		ElapsedMS: elapsedMS,
	}
	cleanup()
	return result, fmt.Errorf("unit %q run: %w", name, err)
}
result.Units[name] = UnitResult{
	Kind:      node.def.Kind,
	Status:    StatusCompleted,
	Outputs:   outputs.resultsForUnit(name, node.def.Outputs),
	Metrics:   env.metrics.results(),
	StartedAt: startedAt,
	EndedAt:   endedAt,
	ElapsedMS: elapsedMS,
}
```

- [ ] **Step 4: Verify kernel timeline test passes**

Run:

```bash
GOWORK=off go test ./benchkit/kernel -run TestEngineRecordsUnitTimeline -count=1
```

Expected: PASS.

- [ ] **Step 5: Write and pass report timing test**

Add this assertion to an existing report test or create `benchkit/report/report_test.go` if the file does not exist:

```go
func TestWriteDirIncludesUnitTiming(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "timed",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"work": {
				Kind:      "test.timeline/v1",
				Status:    kernel.StatusCompleted,
				StartedAt: "2026-06-02T01:02:03Z",
				EndedAt:   "2026-06-02T01:02:04Z",
				ElapsedMS: 1000,
			},
		},
	}
	if err := WriteDir(dir, result); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	got := string(data)
	for _, want := range []string{"elapsed `1000ms`", "started `2026-06-02T01:02:03Z`", "ended `2026-06-02T01:02:04Z`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
}
```

Update `summaryMarkdown` so each unit row includes:

```go
if unit.ElapsedMS > 0 {
	out += fmt.Sprintf("  - timing: elapsed `%dms`, started `%s`, ended `%s`\n", unit.ElapsedMS, unit.StartedAt, unit.EndedAt)
}
```

Run:

```bash
GOWORK=off go test ./benchkit/kernel ./benchkit/report -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go benchkit/report/report.go benchkit/report/report_test.go
git commit -m "feat: record unit timelines"
```

---

### Task 2: Add Background Unit Lifecycle

**Files:**
- Modify: `benchkit/contract/types.go`
- Modify: `benchkit/contract/types_test.go`
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`

- [ ] **Step 1: Write compile-time contract tests**

Create or extend `benchkit/contract/types_test.go`:

```go
package contract_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestBackgroundInterfacesCompile(t *testing.T) {
	var _ contract.BackgroundUnit = backgroundCompileUnit{}
	var _ contract.BackgroundTask = backgroundCompileTask{}
}

type backgroundCompileUnit struct{}

func (backgroundCompileUnit) Definition() contract.Definition { return contract.Definition{Kind: "test.background/v1"} }
func (backgroundCompileUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (backgroundCompileUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (backgroundCompileUnit) Run(context.Context, contract.RunEnv) error { return nil }
func (backgroundCompileUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	return backgroundCompileTask{}, nil
}

type backgroundCompileTask struct{}

func (backgroundCompileTask) Done() <-chan error {
	ch := make(chan error)
	close(ch)
	return ch
}
func (backgroundCompileTask) Stop(context.Context) error { return nil }
```

- [ ] **Step 2: Write background ordering and snapshot tests**

Append these tests and helpers to `benchkit/kernel/kernel_test.go`:

```go
func TestEngineRunsBackgroundUnitDuringForegroundWork(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events})
	reg.MustRegister(foregroundProbeUnit{events: events})
	scenario := dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
			"traffic": {Use: "test.foreground_probe/v1", After: []string{"metrics"}},
		},
	}

	result, err := New(reg).Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := drainEvents(events)
	want := []string{"metrics:start", "traffic:run", "metrics:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
	unit := result.Units["metrics"]
	if unit.Status != StatusCompleted {
		t.Fatalf("background status = %s", unit.Status)
	}
	if unit.Outputs["summary"].Value.(map[string]any)["stopped"] != true {
		t.Fatalf("background output was not snapshotted after Stop: %#v", unit.Outputs)
	}
	if unit.Metrics["ticks_total"].Sum != 1 {
		t.Fatalf("background metrics missing post-foreground emission: %#v", unit.Metrics)
	}
}

func TestEngineStopsBackgroundUnitsInReverseStartOrder(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_a/v1", title: "a", events: events})
	reg.MustRegister(backgroundProbeUnit{kind: "test.bg_b/v1", title: "b", events: events})
	reg.MustRegister(foregroundProbeUnit{events: events})
	scenario := dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "reverse"},
		Units: map[string]dsl.UnitNode{
			"a":       {Use: "test.bg_a/v1"},
			"b":       {Use: "test.bg_b/v1", After: []string{"a"}},
			"traffic": {Use: "test.foreground_probe/v1", After: []string{"b"}},
		},
	}

	_, err := New(reg).Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := drainEvents(events)
	want := []string{"a:start", "b:start", "traffic:run", "b:stop", "a:stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

type backgroundProbeUnit struct {
	kind   string
	title  string
	events chan string
}

func (u backgroundProbeUnit) Definition() contract.Definition {
	kind := u.kind
	if kind == "" {
		kind = "test.background_probe/v1"
	}
	return contract.Definition{
		Kind: kind,
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
func (u backgroundProbeUnit) Start(ctx context.Context, env contract.RunEnv) (contract.BackgroundTask, error) {
	name := env.UnitName()
	u.events <- name + ":start"
	return &backgroundProbeTask{name: name, events: u.events, env: env, done: make(chan error, 1)}, nil
}

type backgroundProbeTask struct {
	name   string
	events chan string
	env    contract.RunEnv
	done   chan error
}

func (t *backgroundProbeTask) Done() <-chan error { return t.done }
func (t *backgroundProbeTask) Stop(context.Context) error {
	t.events <- t.name + ":stop"
	t.env.EmitCounter("ticks_total", 1, nil)
	if err := t.env.SetOutput("summary", map[string]any{"stopped": true}); err != nil {
		return err
	}
	close(t.done)
	return nil
}

type foregroundProbeUnit struct {
	events chan string
}

func (foregroundProbeUnit) Definition() contract.Definition { return contract.Definition{Kind: "test.foreground_probe/v1"} }
func (foregroundProbeUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (foregroundProbeUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (u foregroundProbeUnit) Run(ctx context.Context, env contract.RunEnv) error {
	u.events <- env.UnitName() + ":run"
	return nil
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
```

- [ ] **Step 3: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./benchkit/contract ./benchkit/kernel -run 'TestBackgroundInterfacesCompile|TestEngineRunsBackgroundUnitDuringForegroundWork|TestEngineStopsBackgroundUnitsInReverseStartOrder' -count=1
```

Expected: FAIL because `BackgroundUnit` and `BackgroundTask` are undefined and the kernel does not start background tasks.

- [ ] **Step 4: Add background interfaces**

In `benchkit/contract/types.go`, add after `Unit`:

```go
// BackgroundUnit is an optional lifecycle for units that run while later graph nodes execute.
type BackgroundUnit interface {
	Unit
	// Start starts background work and returns when the unit is ready for downstream units.
	Start(context.Context, RunEnv) (BackgroundTask, error)
}

// BackgroundTask is the active background worker returned by a BackgroundUnit.
type BackgroundTask interface {
	// Done closes when the worker exits. A received non-nil error is fatal to the run.
	Done() <-chan error
	// Stop asks the worker to flush, publish final outputs, and exit.
	Stop(context.Context) error
}
```

- [ ] **Step 5: Implement background start/stop in the kernel**

In `benchkit/kernel/kernel.go`, add this local state:

```go
type activeBackground struct {
	name      string
	node      *graphNode
	env       *runEnv
	task      contract.BackgroundTask
	startedAt time.Time
}
```

In `Run`, keep an `active []activeBackground`. For each graph node, after `env` is created:

```go
if background, ok := node.unit.(contract.BackgroundUnit); ok {
	start := time.Now()
	task, err := background.Start(ctx, env)
	if err != nil {
		end := time.Now()
		startedAt, endedAt, elapsedMS := timelineFields(start, end)
		result.Status = StatusWorkerFailed
		result.Units[name] = UnitResult{
			Kind: node.def.Kind, Status: StatusWorkerFailed, Error: err.Error(),
			Metrics: env.metrics.results(), StartedAt: startedAt, EndedAt: endedAt, ElapsedMS: elapsedMS,
		}
		stopBackgrounds(ctx, active, outputs, result.Units)
		cleanup()
		return result, fmt.Errorf("unit %q start: %w", name, err)
	}
	active = append(active, activeBackground{name: name, node: node, env: env, task: task, startedAt: start})
	continue
}
```

After the foreground loop, stop backgrounds:

```go
if err := stopBackgrounds(ctx, active, outputs, result.Units); err != nil {
	result.Status = StatusWorkerFailed
	cleanup()
	return result, err
}
cleanup()
return result, nil
```

Add the stop helper:

```go
func stopBackgrounds(ctx context.Context, active []activeBackground, outputs *outputStore, results map[string]UnitResult) error {
	var firstErr error
	for i := len(active) - 1; i >= 0; i-- {
		bg := active[i]
		err := bg.task.Stop(ctx)
		end := time.Now()
		startedAt, endedAt, elapsedMS := timelineFields(bg.startedAt, end)
		status := StatusCompleted
		errorText := ""
		if err != nil {
			status = StatusWorkerFailed
			errorText = err.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("unit %q stop: %w", bg.name, err)
			}
		}
		results[bg.name] = UnitResult{
			Kind:      bg.node.def.Kind,
			Status:    status,
			Error:     errorText,
			Outputs:   outputs.resultsForUnit(bg.name, bg.node.def.Outputs),
			Metrics:   bg.env.metrics.results(),
			StartedAt: startedAt,
			EndedAt:   endedAt,
			ElapsedMS: elapsedMS,
		}
	}
	return firstErr
}
```

- [ ] **Step 6: Verify lifecycle tests pass**

Run:

```bash
GOWORK=off go test ./benchkit/contract ./benchkit/kernel -run 'TestBackgroundInterfacesCompile|TestEngineRunsBackgroundUnitDuringForegroundWork|TestEngineStopsBackgroundUnitsInReverseStartOrder' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add benchkit/contract/types.go benchkit/contract/types_test.go benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go
git commit -m "feat: add background unit lifecycle"
```

---

### Task 3: Propagate Background Failures and Foreground Cancellation

**Files:**
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`

- [ ] **Step 1: Write background fatal error test**

Append to `benchkit/kernel/kernel_test.go`:

```go
func TestEngineCancelsForegroundWhenBackgroundFails(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(failingBackgroundUnit{})
	reg.MustRegister(cancelAwareForegroundUnit{})
	scenario := dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "background-fail"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.failing_background/v1"},
			"traffic": {Use: "test.cancel_aware_foreground/v1", After: []string{"metrics"}},
		},
	}

	result, err := New(reg).Run(context.Background(), scenario)
	if err == nil {
		t.Fatalf("expected run error")
	}
	if result.Status != StatusWorkerFailed {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Units["metrics"].Status != StatusWorkerFailed {
		t.Fatalf("metrics status = %s", result.Units["metrics"].Status)
	}
	if result.Units["traffic"].Status != StatusWorkerFailed {
		t.Fatalf("traffic status = %s", result.Units["traffic"].Status)
	}
}

type failingBackgroundUnit struct{}

func (failingBackgroundUnit) Definition() contract.Definition { return contract.Definition{Kind: "test.failing_background/v1"} }
func (failingBackgroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (failingBackgroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (failingBackgroundUnit) Run(context.Context, contract.RunEnv) error { return nil }
func (failingBackgroundUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	done := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		done <- fmt.Errorf("scrape failed")
		close(done)
	}()
	return failingBackgroundTask{done: done}, nil
}

type failingBackgroundTask struct {
	done chan error
}

func (t failingBackgroundTask) Done() <-chan error { return t.done }
func (t failingBackgroundTask) Stop(context.Context) error { return nil }

type cancelAwareForegroundUnit struct{}

func (cancelAwareForegroundUnit) Definition() contract.Definition { return contract.Definition{Kind: "test.cancel_aware_foreground/v1"} }
func (cancelAwareForegroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (cancelAwareForegroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (cancelAwareForegroundUnit) Run(ctx context.Context, env contract.RunEnv) error {
	<-ctx.Done()
	return ctx.Err()
}
```

- [ ] **Step 2: Write foreground failure still stops background test**

Append:

```go
func TestEngineStopsBackgroundWhenForegroundFails(t *testing.T) {
	events := make(chan string, 8)
	reg := registry.New()
	reg.MustRegister(backgroundProbeUnit{events: events})
	reg.MustRegister(failingForegroundUnit{})
	scenario := dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "foreground-fail"},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.background_probe/v1"},
			"traffic": {Use: "test.failing_foreground/v1", After: []string{"metrics"}},
		},
	}

	result, err := New(reg).Run(context.Background(), scenario)
	if err == nil {
		t.Fatalf("expected run error")
	}
	got := strings.Join(drainEvents(events), ",")
	if !strings.Contains(got, "metrics:stop") {
		t.Fatalf("background was not stopped, events=%s", got)
	}
	if result.Units["metrics"].Status != StatusCompleted {
		t.Fatalf("background partial result not recorded: %#v", result.Units["metrics"])
	}
}

type failingForegroundUnit struct{}

func (failingForegroundUnit) Definition() contract.Definition { return contract.Definition{Kind: "test.failing_foreground/v1"} }
func (failingForegroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (failingForegroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (failingForegroundUnit) Run(context.Context, contract.RunEnv) error {
	return fmt.Errorf("send failed")
}
```

- [ ] **Step 3: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./benchkit/kernel -run 'TestEngineCancelsForegroundWhenBackgroundFails|TestEngineStopsBackgroundWhenForegroundFails' -count=1
```

Expected: FAIL because the kernel does not monitor `BackgroundTask.Done` and foreground failure does not snapshot active background units.

- [ ] **Step 4: Use a child context and monitor background errors**

At the start of `Run`, after graph creation succeeds, create a child context:

```go
runCtx, cancel := context.WithCancel(ctx)
defer cancel()
backgroundErrors := make(chan backgroundError, len(scenario.Units))
```

Add:

```go
type backgroundError struct {
	unit string
	err  error
}
```

When a background task starts, monitor it:

```go
go func(unitName string, task contract.BackgroundTask) {
	errCh := task.Done()
	if errCh == nil {
		return
	}
	if err, ok := <-errCh; ok && err != nil {
		backgroundErrors <- backgroundError{unit: unitName, err: err}
		cancel()
	}
}(name, task)
```

Pass `runCtx` to `Validate`, `Plan`, foreground `Run`, background `Start`, and `Stop`. Before each normal foreground unit starts, check:

```go
select {
case failed := <-backgroundErrors:
	result.Status = StatusWorkerFailed
	stopBackgrounds(runCtx, active, outputs, result.Units)
	cleanup()
	return result, fmt.Errorf("unit %q background: %w", failed.unit, failed.err)
default:
}
```

When a foreground unit returns an error, call `cancel()`, then `stopBackgrounds`, then return the foreground error. If `runCtx.Err() != nil`, keep the foreground unit result as `worker_failed` with `ctx.Err().Error()`.

- [ ] **Step 5: Record the failing background result**

Change `stopBackgrounds` to accept a fatal map:

```go
fatalBackgrounds := make(map[string]error)
```

When receiving from `backgroundErrors`, set `fatalBackgrounds[failed.unit] = failed.err`. In `stopBackgrounds`, if `fatalBackgrounds[bg.name]` exists, mark that unit `StatusWorkerFailed` and use the fatal error text even if `Stop` succeeds.

- [ ] **Step 6: Verify failure tests pass**

Run:

```bash
GOWORK=off go test ./benchkit/kernel -run 'TestEngineCancelsForegroundWhenBackgroundFails|TestEngineStopsBackgroundWhenForegroundFails|TestEngineRunsBackgroundUnitDuringForegroundWork' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go
git commit -m "feat: propagate background failures"
```

---

### Task 4: Add Declared Artifact Support

**Files:**
- Modify: `benchkit/contract/types.go`
- Modify: `benchkit/contract/types_test.go`
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`
- Modify: `benchkit/report/report.go`
- Modify: `benchkit/report/report_test.go`

- [ ] **Step 1: Write `TestRunEnv.OpenArtifact` tests**

Add to `benchkit/contract/types_test.go`:

```go
func TestTestRunEnvOpenArtifactWritesTempFile(t *testing.T) {
	env := contract.NewTestRunEnv("run", "unit", nil, nil)
	env.DeclareArtifacts([]contract.ArtifactDef{{Name: "metrics.jsonl", ContentType: "application/jsonl"}})
	w, err := env.OpenArtifact("metrics.jsonl")
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	if _, err := w.Write([]byte("{\"ok\":true}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	artifacts := env.Artifacts()
	if artifacts["metrics.jsonl"].SizeBytes == 0 {
		t.Fatalf("artifact metadata not recorded: %#v", artifacts)
	}
}

func TestTestRunEnvRejectsUndeclaredArtifact(t *testing.T) {
	env := contract.NewTestRunEnv("run", "unit", nil, nil)
	_, err := env.OpenArtifact("metrics.jsonl")
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected undeclared artifact error, got %v", err)
	}
}
```

- [ ] **Step 2: Write kernel artifact tests**

Append to `benchkit/kernel/kernel_test.go`:

```go
func TestEngineWritesDeclaredArtifacts(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New()
	reg.MustRegister(artifactUnit{})
	scenario := dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "artifacts", ReportDir: dir},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.artifact/v1"},
		},
	}

	result, err := New(reg).Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	artifact := result.Units["metrics"].Artifacts["metrics.jsonl"]
	if artifact.Path != "artifacts/metrics/metrics.jsonl" {
		t.Fatalf("artifact path = %q", artifact.Path)
	}
	if artifact.SizeBytes == 0 {
		t.Fatalf("artifact size not recorded: %#v", artifact)
	}
	if _, err := os.Stat(filepath.Join(dir, artifact.Path)); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
}

func TestEngineRejectsUndeclaredArtifact(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New()
	reg.MustRegister(undeclaredArtifactUnit{})
	scenario := dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "bad-artifact", ReportDir: dir},
		Units: map[string]dsl.UnitNode{
			"metrics": {Use: "test.undeclared_artifact/v1"},
		},
	}

	result, err := New(reg).Run(context.Background(), scenario)
	if err == nil {
		t.Fatalf("expected run error")
	}
	if result.Units["metrics"].Status != StatusWorkerFailed {
		t.Fatalf("unit status = %s", result.Units["metrics"].Status)
	}
}

type artifactUnit struct{}

func (artifactUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.artifact/v1",
		Artifacts: []contract.ArtifactDef{{
			Name: "metrics.jsonl", ContentType: "application/jsonl",
		}},
	}
}
func (artifactUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (artifactUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) { return contract.Plan{}, nil }
func (artifactUnit) Run(ctx context.Context, env contract.RunEnv) error {
	w, err := env.OpenArtifact("metrics.jsonl")
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = w.Write([]byte("{\"scrape\":1}\n"))
	return err
}

type undeclaredArtifactUnit struct{}

func (undeclaredArtifactUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.undeclared_artifact/v1"}
}
func (undeclaredArtifactUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (undeclaredArtifactUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (undeclaredArtifactUnit) Run(ctx context.Context, env contract.RunEnv) error {
	w, err := env.OpenArtifact("metrics.jsonl")
	if err != nil {
		return err
	}
	return w.Close()
}
```

- [ ] **Step 3: Run artifact tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./benchkit/contract ./benchkit/kernel -run 'TestTestRunEnvOpenArtifactWritesTempFile|TestTestRunEnvRejectsUndeclaredArtifact|TestEngineWritesDeclaredArtifacts|TestEngineRejectsUndeclaredArtifact' -count=1
```

Expected: FAIL because `RunEnv.OpenArtifact`, `DeclareArtifacts`, `Artifacts`, and `UnitResult.Artifacts` do not exist.

- [ ] **Step 4: Extend contract artifact types and test env**

In `benchkit/contract/types.go`, extend `ArtifactDef`:

```go
type ArtifactDef struct {
	// Name is the unit-local artifact name.
	Name string
	// ContentType is the optional MIME type recorded in reports.
	ContentType string
}
```

Extend `RunEnv`:

```go
	// OpenArtifact opens a declared artifact for writing.
	OpenArtifact(name string) (io.WriteCloser, error)
```

Add `io`, `os`, and `path/filepath` imports. Add artifact state to `TestRunEnv`:

```go
	artifactDefs map[string]ArtifactDef
	artifactDir  string
	artifacts    map[string]ArtifactInfo
```

Add this public test metadata type:

```go
// ArtifactInfo is artifact metadata captured by TestRunEnv.
type ArtifactInfo struct {
	Path        string
	ContentType string
	SizeBytes   int64
}
```

Add methods:

```go
func (e *TestRunEnv) DeclareArtifacts(defs []ArtifactDef) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artifactDefs = make(map[string]ArtifactDef, len(defs))
	for _, def := range defs {
		e.artifactDefs[def.Name] = def
	}
}

func (e *TestRunEnv) Artifacts() map[string]ArtifactInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]ArtifactInfo, len(e.artifacts))
	for name, artifact := range e.artifacts {
		out[name] = artifact
	}
	return out
}

func (e *TestRunEnv) OpenArtifact(name string) (io.WriteCloser, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	def, ok := e.artifactDefs[name]
	if !ok {
		return nil, fmt.Errorf("artifact %q is not declared", name)
	}
	if e.artifactDir == "" {
		e.artifactDir = os.TempDir()
	}
	path := filepath.Join(e.artifactDir, e.unitName+"-"+name)
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if e.artifacts == nil {
		e.artifacts = make(map[string]ArtifactInfo)
	}
	return &artifactWriteCloser{
		WriteCloser: file,
		onClose: func(size int64) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.artifacts[name] = ArtifactInfo{Path: path, ContentType: def.ContentType, SizeBytes: size}
		},
	}, nil
}
```

Add this small wrapper:

```go
type artifactWriteCloser struct {
	io.WriteCloser
	written int64
	onClose func(int64)
}

func (w *artifactWriteCloser) Write(data []byte) (int, error) {
	n, err := w.WriteCloser.Write(data)
	w.written += int64(n)
	return n, err
}

func (w *artifactWriteCloser) Close() error {
	err := w.WriteCloser.Close()
	w.onClose(w.written)
	return err
}
```

- [ ] **Step 5: Add kernel artifact metadata and writer**

In `benchkit/kernel/kernel.go`, add:

```go
type ArtifactResult struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
}
```

Add to `UnitResult`:

```go
	// Artifacts lists files written by the unit.
	Artifacts map[string]ArtifactResult `json:"artifacts,omitempty"`
```

Extend `runEnv`:

```go
	artifactDefs map[string]contract.ArtifactDef
	artifacts    map[string]ArtifactResult
```

Initialize `artifactDefs` from `node.def.Artifacts` when constructing `runEnv`. Implement:

```go
func (e *runEnv) OpenArtifact(name string) (io.WriteCloser, error) {
	def, ok := e.artifactDefs[name]
	if !ok {
		return nil, fmt.Errorf("artifact %q is not declared", name)
	}
	if !isSimpleArtifactName(name) {
		return nil, fmt.Errorf("artifact %q must be a simple file name", name)
	}
	if strings.TrimSpace(e.scenario.Run.ReportDir) == "" {
		return nil, fmt.Errorf("run.report_dir is required to write artifact %q", name)
	}
	relPath := filepath.ToSlash(filepath.Join("artifacts", e.unitName, name))
	fullPath := filepath.Join(e.scenario.Run.ReportDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, err
	}
	file, err := os.Create(fullPath)
	if err != nil {
		return nil, err
	}
	return &kernelArtifactWriter{file: file, env: e, name: name, relPath: relPath, contentType: def.ContentType}, nil
}

func isSimpleArtifactName(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.Contains(name, "..")
}
```

Add writer:

```go
type kernelArtifactWriter struct {
	file        *os.File
	env         *runEnv
	name        string
	relPath     string
	contentType string
	size        int64
}

func (w *kernelArtifactWriter) Write(data []byte) (int, error) {
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *kernelArtifactWriter) Close() error {
	err := w.file.Close()
	w.env.mu.Lock()
	defer w.env.mu.Unlock()
	if w.env.artifacts == nil {
		w.env.artifacts = make(map[string]ArtifactResult)
	}
	w.env.artifacts[w.name] = ArtifactResult{Path: w.relPath, ContentType: w.contentType, SizeBytes: w.size}
	return err
}
```

Add:

```go
func (e *runEnv) artifactResults() map[string]ArtifactResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.artifacts) == 0 {
		return nil
	}
	out := make(map[string]ArtifactResult, len(e.artifacts))
	for name, artifact := range e.artifacts {
		out[name] = artifact
	}
	return out
}
```

Set `Artifacts: env.artifactResults()` in normal and background `UnitResult` creation.

- [ ] **Step 6: Render artifact rows in summary**

Add to `benchkit/report/report.go` within the per-unit loop:

```go
artifactNames := make([]string, 0, len(unit.Artifacts))
for artifactName := range unit.Artifacts {
	artifactNames = append(artifactNames, artifactName)
}
sort.Strings(artifactNames)
for _, artifactName := range artifactNames {
	artifact := unit.Artifacts[artifactName]
	out += fmt.Sprintf("  - artifact `%s`: `%s`, %s\n", artifactName, artifact.Path, formatBytes(artifact.SizeBytes))
}
```

Add:

```go
func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}
```

Add a report test:

```go
func TestWriteDirIncludesArtifactRows(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID: "artifacts",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"metrics": {
				Kind: "wukongim.metrics_collector/v1",
				Status: kernel.StatusCompleted,
				Artifacts: map[string]kernel.ArtifactResult{
					"metrics.jsonl": {Path: "artifacts/metrics/metrics.jsonl", ContentType: "application/jsonl", SizeBytes: 2048},
				},
			},
		},
	}
	if err := WriteDir(dir, result); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	got := string(data)
	for _, want := range []string{"artifact `metrics.jsonl`", "artifacts/metrics/metrics.jsonl", "2.0KB"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 7: Verify artifact support**

Run:

```bash
GOWORK=off go test ./benchkit/contract ./benchkit/kernel ./benchkit/report -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add benchkit/contract/types.go benchkit/contract/types_test.go benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go benchkit/report/report.go benchkit/report/report_test.go
git commit -m "feat: support unit artifacts"
```

---

### Task 5: Add WuKongIM Metrics Summary Port

**Files:**
- Create: `benchkit/ports/wukongim/metrics_summary.go`
- Create: `benchkit/ports/wukongim/metrics_summary_test.go`
- Modify: `benchkit/report/report.go`

- [ ] **Step 1: Write summary port tests**

Create `benchkit/ports/wukongim/metrics_summary_test.go`:

```go
package wukongim_test

import (
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
)

func TestMetricsSummaryPortContract(t *testing.T) {
	if wukongimport.MetricsSummaryV1 != contract.PortType("port.wukongim.metrics_summary/v1") {
		t.Fatalf("unexpected port type %q", wukongimport.MetricsSummaryV1)
	}
	summary := wukongimport.MetricsSummary{
		ScrapeTicks:       3,
		SelectedSamples:   12,
		DroppedMetricNames: 2,
		Nodes: []wukongimport.NodeScrapeSummary{{
			Address: "http://127.0.0.1:5011",
			Success: 2,
			Errors:  1,
		}},
		LatencyP95MS: 8.5,
		LatencyP99MS: 12.0,
		Latest: []wukongimport.MetricSampleSummary{{
			Name:   "wk_channel_active",
			Labels: map[string]string{"node": "1"},
			Value:  7,
		}},
	}
	report := summary.ReportOutput().(map[string]any)
	if report["scrape_ticks"] != int64(3) || report["selected_samples"] != int64(12) {
		t.Fatalf("unexpected report: %#v", report)
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
GOWORK=off go test ./benchkit/ports/wukongim -run TestMetricsSummaryPortContract -count=1
```

Expected: FAIL because the port package file does not exist.

- [ ] **Step 3: Implement the summary port**

Create `benchkit/ports/wukongim/metrics_summary.go`:

```go
// Package wukongim defines WuKongIM-specific wkbench ports.
package wukongim

import "github.com/WuKongIM/wkbench/benchkit/contract"

// MetricsSummaryV1 is the port type for compact WuKongIM metrics scrape summaries.
const MetricsSummaryV1 contract.PortType = "port.wukongim.metrics_summary/v1"

// MetricsSummary is a bounded, reportable summary of scraped WuKongIM metrics.
type MetricsSummary struct {
	ScrapeTicks        int64                 `json:"scrape_ticks"`
	SelectedSamples    int64                 `json:"selected_samples"`
	DroppedMetricNames int64                 `json:"dropped_metric_names,omitempty"`
	Nodes              []NodeScrapeSummary   `json:"nodes,omitempty"`
	LatencyP95MS       float64               `json:"latency_p95_ms,omitempty"`
	LatencyP99MS       float64               `json:"latency_p99_ms,omitempty"`
	Latest             []MetricSampleSummary `json:"latest,omitempty"`
}

// NodeScrapeSummary contains per-node scrape health.
type NodeScrapeSummary struct {
	Address string `json:"address"`
	Success int64  `json:"success"`
	Errors  int64  `json:"errors"`
}

// MetricSampleSummary contains the latest selected value for one metric series.
type MetricSampleSummary struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
}

// ReportOutput returns a JSON-friendly compact report payload.
func (s MetricsSummary) ReportOutput() any {
	return map[string]any{
		"scrape_ticks":          s.ScrapeTicks,
		"selected_samples":      s.SelectedSamples,
		"dropped_metric_names":  s.DroppedMetricNames,
		"nodes":                 s.Nodes,
		"latency_p95_ms":        s.LatencyP95MS,
		"latency_p99_ms":        s.LatencyP99MS,
		"latest":                s.Latest,
	}
}
```

- [ ] **Step 4: Render metrics summary compactly in Markdown**

In `benchkit/report/report.go`, import:

```go
wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
```

Extend `formatOutputValue`:

```go
case wukongimport.MetricsSummary:
	return fmt.Sprintf("scrapes: `%d`, errors: `%d`, samples: `%d`, latency_p95: `%.2fms`, latency_p99: `%.2fms`",
		v.ScrapeTicks, metricSummaryErrors(v), v.SelectedSamples, v.LatencyP95MS, v.LatencyP99MS)
```

Add:

```go
func metricSummaryErrors(summary wukongimport.MetricsSummary) int64 {
	var total int64
	for _, node := range summary.Nodes {
		total += node.Errors
	}
	return total
}
```

- [ ] **Step 5: Verify summary port**

Run:

```bash
GOWORK=off go test ./benchkit/ports/wukongim ./benchkit/report -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add benchkit/ports/wukongim/metrics_summary.go benchkit/ports/wukongim/metrics_summary_test.go benchkit/report/report.go
git commit -m "feat: add wukongim metrics summary port"
```

---

### Task 6: Implement Metrics Collector Validation and Parser

**Files:**
- Create: `units/wukongim/metrics_collector/unit.go`
- Create: `units/wukongim/metrics_collector/prometheus.go`
- Create: `units/wukongim/metrics_collector/unit_test.go`

- [ ] **Step 1: Write validation and parser tests**

Create `units/wukongim/metrics_collector/unit_test.go`:

```go
package metrics_collector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestValidateRejectsInvalidSpec(t *testing.T) {
	tests := []struct {
		name string
		spec map[string]any
		want string
	}{
		{name: "zero interval", spec: map[string]any{"interval": "0s"}, want: "interval"},
		{name: "bad timeout", spec: map[string]any{"interval": "1s", "timeout": "0s"}, want: "timeout"},
		{name: "bad include regex", spec: map[string]any{"interval": "1s", "include": []any{"["}}, want: "include"},
		{name: "bad exclude regex", spec: map[string]any{"interval": "1s", "exclude": []any{"["}}, want: "exclude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := contract.NewTestRunEnv("run", "metrics", nil, tt.spec)
			err := Unit{}.Validate(context.Background(), env)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestParsePrometheusTextFiltersMetrics(t *testing.T) {
	spec := collectorSpec{
		Include: []string{"wk_.*"},
		Exclude: []string{"wk_skip_.*"},
	}
	filter, err := newMetricFilter(spec)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	samples, parseErrors := parsePrometheusText([]byte(`
# HELP wk_active Active count
wk_active{node="1"} 7
wk_skip_total 9
go_threads 12
bad line
`), filter)
	if parseErrors != 1 {
		t.Fatalf("parse errors = %d", parseErrors)
	}
	if len(samples) != 1 {
		t.Fatalf("samples = %#v", samples)
	}
	if samples[0].Name != "wk_active" || samples[0].Labels["node"] != "1" || samples[0].Value != 7 {
		t.Fatalf("unexpected sample: %#v", samples[0])
	}
}
```

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./units/wukongim/metrics_collector -run 'TestValidateRejectsInvalidSpec|TestParsePrometheusTextFiltersMetrics' -count=1
```

Expected: FAIL because the collector package does not exist.

- [ ] **Step 3: Implement unit definition and validation**

Create `units/wukongim/metrics_collector/unit.go`:

```go
package metrics_collector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const Kind = "wukongim.metrics_collector/v1"

type Unit struct{}

type collectorSpec struct {
	Interval             contract.Duration `json:"interval" yaml:"interval"`
	Timeout              contract.Duration `json:"timeout" yaml:"timeout"`
	Path                 string            `json:"path" yaml:"path"`
	Include              []string          `json:"include" yaml:"include"`
	Exclude              []string          `json:"exclude" yaml:"exclude"`
	FailOnScrapeError    bool              `json:"fail_on_scrape_error" yaml:"fail_on_scrape_error"`
	MaxConsecutiveErrors  int               `json:"max_consecutive_errors" yaml:"max_consecutive_errors"`
	MaxSummaryMetrics     int               `json:"max_summary_metrics" yaml:"max_summary_metrics"`
}

func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        Kind,
		Title:       "WuKongIM Metrics Collector",
		Description: "Scrapes WuKongIM /metrics while foreground workloads run.",
		Inputs:      []contract.PortDef{{Name: "target", Type: targetport.TargetV1}},
		Outputs:     []contract.PortDef{{Name: "summary", Type: wukongimport.MetricsSummaryV1}},
		Metrics: []contract.MetricDef{
			{Name: "scrape_success_total", Type: "counter"},
			{Name: "scrape_error_total", Type: "counter"},
			{Name: "scrape_parse_error_total", Type: "counter"},
			{Name: "scrape_latency", Type: "duration"},
		},
		Artifacts: []contract.ArtifactDef{{Name: "metrics.jsonl", ContentType: "application/jsonl"}},
	}
}

func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	_, err = newMetricFilter(spec)
	return err
}

func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	spec, err := decodeSpec(env)
	if err != nil {
		return contract.Plan{}, err
	}
	return contract.Plan{UnitName: env.UnitName(), Shards: []any{map[string]any{
		"interval": spec.Interval.Duration.String(),
		"path":     spec.Path,
	}}}, nil
}

func (Unit) Run(context.Context, contract.RunEnv) error {
	return fmt.Errorf("%s is a background unit; use Start", Kind)
}

func decodeSpec(env contract.ValidateEnv) (collectorSpec, error) {
	spec := collectorSpec{
		Interval: contract.Duration{Duration: time.Second},
		Timeout: contract.Duration{Duration: 800 * time.Millisecond},
		Path: "/metrics",
		MaxSummaryMetrics: 100,
	}
	if err := env.DecodeSpec(&spec); err != nil {
		return collectorSpec{}, err
	}
	if spec.Interval.Duration <= 0 {
		return collectorSpec{}, fmt.Errorf("interval must be greater than zero")
	}
	if spec.Timeout.Duration <= 0 {
		return collectorSpec{}, fmt.Errorf("timeout must be greater than zero")
	}
	if strings.TrimSpace(spec.Path) == "" || !strings.HasPrefix(spec.Path, "/") {
		return collectorSpec{}, fmt.Errorf("path must start with /")
	}
	if spec.MaxSummaryMetrics <= 0 {
		spec.MaxSummaryMetrics = 100
	}
	if spec.MaxConsecutiveErrors < 0 {
		return collectorSpec{}, fmt.Errorf("max_consecutive_errors must be non-negative")
	}
	return spec, nil
}
```

- [ ] **Step 4: Implement small Prometheus parser**

Create `units/wukongim/metrics_collector/prometheus.go`:

```go
package metrics_collector

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type metricSample struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
}

type metricFilter struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

func newMetricFilter(spec collectorSpec) (metricFilter, error) {
	include, err := compileRegexes("include", spec.Include)
	if err != nil {
		return metricFilter{}, err
	}
	exclude, err := compileRegexes("exclude", spec.Exclude)
	if err != nil {
		return metricFilter{}, err
	}
	return metricFilter{include: include, exclude: exclude}, nil
}

func compileRegexes(field string, patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s regex %q: %w", field, pattern, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func (f metricFilter) allow(name string) bool {
	included := len(f.include) == 0
	for _, re := range f.include {
		if re.MatchString(name) {
			included = true
			break
		}
	}
	if !included {
		return false
	}
	for _, re := range f.exclude {
		if re.MatchString(name) {
			return false
		}
	}
	return true
}

func parsePrometheusText(data []byte, filter metricFilter) ([]metricSample, int64) {
	lines := bytes.Split(data, []byte("\n"))
	samples := make([]metricSample, 0, len(lines))
	var parseErrors int64
	for _, rawLine := range lines {
		line := strings.TrimSpace(string(rawLine))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sample, ok := parseMetricLine(line)
		if !ok {
			parseErrors++
			continue
		}
		if filter.allow(sample.Name) {
			samples = append(samples, sample)
		}
	}
	return samples, parseErrors
}

func parseMetricLine(line string) (metricSample, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return metricSample{}, false
	}
	nameLabels := fields[0]
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return metricSample{}, false
	}
	name, labels, ok := splitNameLabels(nameLabels)
	if !ok || name == "" {
		return metricSample{}, false
	}
	return metricSample{Name: name, Labels: labels, Value: value}, true
}

func splitNameLabels(raw string) (string, map[string]string, bool) {
	open := strings.IndexByte(raw, '{')
	if open < 0 {
		return raw, nil, true
	}
	if !strings.HasSuffix(raw, "}") {
		return "", nil, false
	}
	name := raw[:open]
	body := strings.TrimSuffix(raw[open+1:], "}")
	labels := make(map[string]string)
	if strings.TrimSpace(body) == "" {
		return name, labels, true
	}
	for _, part := range strings.Split(body, ",") {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			return "", nil, false
		}
		labels[strings.TrimSpace(pair[0])] = strings.Trim(strings.TrimSpace(pair[1]), `"`)
	}
	return name, labels, true
}
```

- [ ] **Step 5: Verify validation and parser**

Run:

```bash
GOWORK=off go test ./units/wukongim/metrics_collector -run 'TestValidateRejectsInvalidSpec|TestParsePrometheusTextFiltersMetrics' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add units/wukongim/metrics_collector/unit.go units/wukongim/metrics_collector/prometheus.go units/wukongim/metrics_collector/unit_test.go
git commit -m "feat: validate wukongim metrics collector"
```

---

### Task 7: Implement Metrics Collector Background Worker

**Files:**
- Modify: `units/wukongim/metrics_collector/unit.go`
- Create: `units/wukongim/metrics_collector/collector.go`
- Modify: `units/wukongim/metrics_collector/unit_test.go`

- [ ] **Step 1: Write scrape success and JSONL artifact test**

Append to `units/wukongim/metrics_collector/unit_test.go`:

```go
func TestCollectorScrapesAndPublishesSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		fmt.Fprintln(w, `wk_active{node="1"} 7`)
	}))
	defer server.Close()

	target := targetport.Target{APIAddrs: []string{server.URL}}
	env := contract.NewTestRunEnv("run", "metrics", map[string]any{"target": target}, map[string]any{
		"interval": "10ms",
		"timeout": "200ms",
		"include": []any{"wk_.*"},
	})
	env.DeclareArtifacts(Unit{}.Definition().Artifacts)

	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	output, ok := env.Output("summary")
	if !ok {
		t.Fatalf("summary output missing")
	}
	summary := output.(wukongimport.MetricsSummary)
	if summary.ScrapeTicks == 0 || summary.SelectedSamples == 0 {
		t.Fatalf("summary did not capture scrapes: %#v", summary)
	}
	if env.Counters()["scrape_success_total"] == 0 {
		t.Fatalf("success counter missing: %#v", env.Counters())
	}
	if len(env.Artifacts()) != 1 {
		t.Fatalf("artifact missing: %#v", env.Artifacts())
	}
}
```

- [ ] **Step 2: Write non-strict and strict scrape error tests**

Append:

```go
func TestCollectorScrapeErrorsAreNonFatalByDefault(t *testing.T) {
	env := contract.NewTestRunEnv("run", "metrics", map[string]any{
		"target": targetport.Target{APIAddrs: []string{"http://127.0.0.1:1"}},
	}, map[string]any{"interval": "10ms", "timeout": "5ms"})
	env.DeclareArtifacts(Unit{}.Definition().Artifacts)
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	select {
	case err := <-task.Done():
		t.Fatalf("non-strict collector exited early: %v", err)
	default:
	}
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if env.Counters()["scrape_error_total"] == 0 {
		t.Fatalf("scrape errors not counted")
	}
}

func TestCollectorStrictScrapeErrorIsFatal(t *testing.T) {
	env := contract.NewTestRunEnv("run", "metrics", map[string]any{
		"target": targetport.Target{APIAddrs: []string{"http://127.0.0.1:1"}},
	}, map[string]any{
		"interval": "10ms",
		"timeout": "5ms",
		"fail_on_scrape_error": true,
		"max_consecutive_errors": 1,
	})
	env.DeclareArtifacts(Unit{}.Definition().Artifacts)
	task, err := Unit{}.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case err := <-task.Done():
		if err == nil || !strings.Contains(err.Error(), "scrape") {
			t.Fatalf("unexpected fatal error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("strict collector did not report fatal error")
	}
	_ = task.Stop(context.Background())
}
```

- [ ] **Step 3: Run worker tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./units/wukongim/metrics_collector -run 'TestCollectorScrapesAndPublishesSummary|TestCollectorScrapeErrorsAreNonFatalByDefault|TestCollectorStrictScrapeErrorIsFatal' -count=1
```

Expected: FAIL because `Unit.Start` and the worker implementation do not exist.

- [ ] **Step 4: Implement `Start`**

Add to `units/wukongim/metrics_collector/unit.go`:

```go
func (u Unit) Start(ctx context.Context, env contract.RunEnv) (contract.BackgroundTask, error) {
	spec, err := decodeSpec(env)
	if err != nil {
		return nil, err
	}
	target, err := contract.Input[targetport.Target](env, "target")
	if err != nil {
		return nil, err
	}
	filter, err := newMetricFilter(spec)
	if err != nil {
		return nil, err
	}
	w, err := env.OpenArtifact("metrics.jsonl")
	if err != nil {
		return nil, err
	}
	collector := newCollector(spec, target, filter, env, w)
	collector.start(ctx)
	return collector, nil
}
```

- [ ] **Step 5: Implement collector worker**

Create `units/wukongim/metrics_collector/collector.go`:

```go
package metrics_collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
)

type collector struct {
	spec   collectorSpec
	target targetport.Target
	filter metricFilter
	env    contract.RunEnv
	out    io.WriteCloser

	ctx    context.Context
	cancel context.CancelFunc
	fatal  chan error
	stopped chan struct{}
	stopOnce sync.Once
	closeOnce sync.Once
	closeErr error
	mu      sync.Mutex
	terminalErr error
	state   summaryState
}

type summaryState struct {
	ticks             int64
	selectedSamples   int64
	parseErrors       int64
	consecutiveErrors int
	latencies         []float64
	nodes             map[string]wukongimport.NodeScrapeSummary
	latest            map[string]wukongimport.MetricSampleSummary
	droppedNames      int64
}

func newCollector(spec collectorSpec, target targetport.Target, filter metricFilter, env contract.RunEnv, out io.WriteCloser) *collector {
	return &collector{
		spec: spec, target: target, filter: filter, env: env, out: out,
		fatal: make(chan error, 1),
		stopped: make(chan struct{}),
		state: summaryState{
			nodes: make(map[string]wukongimport.NodeScrapeSummary),
			latest: make(map[string]wukongimport.MetricSampleSummary),
		},
	}
}

func (c *collector) start(parent context.Context) {
	c.ctx, c.cancel = context.WithCancel(parent)
	go c.loop()
}

func (c *collector) Done() <-chan error { return c.fatal }

func (c *collector) Stop(ctx context.Context) error {
	c.stopOnce.Do(func() { c.cancel() })
	select {
	case <-c.stopped:
		return c.closeAndPublish()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *collector) loop() {
	defer close(c.stopped)
	ticker := time.NewTicker(c.spec.Interval.Duration)
	defer ticker.Stop()
	if err := c.scrapeTick(); err != nil {
		c.finish(err)
		return
	}
	for {
		select {
		case <-c.ctx.Done():
			c.finish(nil)
			return
		case <-ticker.C:
			if err := c.scrapeTick(); err != nil {
				c.finish(err)
				return
			}
		}
	}
}

func (c *collector) finish(err error) {
	c.mu.Lock()
	c.terminalErr = err
	c.mu.Unlock()
	if err != nil {
		c.fatal <- err
	}
	close(c.fatal)
}

func (c *collector) closeAndPublish() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.out.Close()
		if err := c.env.SetOutput("summary", c.summary()); c.closeErr == nil {
			c.closeErr = err
		}
	})
	c.mu.Lock()
	terminalErr := c.terminalErr
	c.mu.Unlock()
	if terminalErr != nil {
		return terminalErr
	}
	return c.closeErr
}

func (c *collector) scrapeTick() error {
	c.mu.Lock()
	c.state.ticks++
	c.mu.Unlock()
	var tickErrors int
	for index, addr := range c.target.APIAddrs {
		if err := c.scrapeAddress(index, addr); err != nil {
			tickErrors++
		}
	}
	if tickErrors == 0 {
		c.mu.Lock()
		c.state.consecutiveErrors = 0
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	c.state.consecutiveErrors++
	shouldFail := c.spec.FailOnScrapeError && c.spec.MaxConsecutiveErrors > 0 && c.state.consecutiveErrors >= c.spec.MaxConsecutiveErrors
	c.mu.Unlock()
	if shouldFail {
		return fmt.Errorf("scrape failed for %d address(es)", tickErrors)
	}
	return nil
}

func (c *collector) scrapeAddress(index int, addr string) error {
	start := time.Now()
	ctx, cancel := context.WithTimeout(c.ctx, c.spec.Timeout.Duration)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL(addr, c.spec.Path), nil)
	if err != nil {
		c.recordError(addr, time.Since(start), err)
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.recordError(addr, time.Since(start), err)
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.recordError(addr, time.Since(start), err)
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("status %d", resp.StatusCode)
		c.recordError(addr, time.Since(start), err)
		return err
	}
	samples, parseErrors := parsePrometheusText(data, c.filter)
	c.recordSuccess(index, addr, time.Since(start), samples, parseErrors)
	return nil
}

func metricsURL(base, metricsPath string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + metricsPath
	}
	u.Path = path.Join(u.Path, metricsPath)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	return u.String()
}
```

- [ ] **Step 6: Implement recording, JSONL, and summary methods**

Append to `collector.go`:

```go
type scrapeRecord struct {
	Timestamp  string         `json:"timestamp"`
	NodeIndex  int            `json:"node_index"`
	Address    string         `json:"address"`
	DurationMS int64          `json:"duration_ms"`
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
	Samples    []metricSample `json:"samples,omitempty"`
}

func (c *collector) recordError(addr string, latency time.Duration, err error) {
	c.env.EmitCounter("scrape_error_total", 1, nil)
	c.env.ObserveDuration("scrape_latency", latency, nil)
	c.writeRecord(scrapeRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Address: addr, DurationMS: latency.Milliseconds(), Status: "error", Error: err.Error(),
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	node := c.state.nodes[addr]
	node.Address = addr
	node.Errors++
	c.state.nodes[addr] = node
	c.state.latencies = append(c.state.latencies, float64(latency.Milliseconds()))
}

func (c *collector) recordSuccess(index int, addr string, latency time.Duration, samples []metricSample, parseErrors int64) {
	c.env.EmitCounter("scrape_success_total", 1, nil)
	if parseErrors > 0 {
		c.env.EmitCounter("scrape_parse_error_total", float64(parseErrors), nil)
	}
	c.env.ObserveDuration("scrape_latency", latency, nil)
	c.writeRecord(scrapeRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		NodeIndex: index, Address: addr, DurationMS: latency.Milliseconds(), Status: "success", Samples: samples,
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	node := c.state.nodes[addr]
	node.Address = addr
	node.Success++
	c.state.nodes[addr] = node
	c.state.selectedSamples += int64(len(samples))
	c.state.parseErrors += parseErrors
	c.state.latencies = append(c.state.latencies, float64(latency.Milliseconds()))
	for _, sample := range samples {
		key := sample.Name + labelsKey(sample.Labels)
		if len(c.state.latest) >= c.spec.MaxSummaryMetrics {
			if _, ok := c.state.latest[key]; !ok {
				c.state.droppedNames++
				continue
			}
		}
		c.state.latest[key] = wukongimport.MetricSampleSummary{Name: sample.Name, Labels: sample.Labels, Value: sample.Value}
	}
}

func (c *collector) writeRecord(record scrapeRecord) {
	data, err := json.Marshal(record)
	if err != nil {
		c.env.EmitCounter("scrape_error_total", 1, nil)
		return
	}
	_, _ = c.out.Write(append(data, '\n'))
}

func (c *collector) summary() wukongimport.MetricsSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	nodes := make([]wukongimport.NodeScrapeSummary, 0, len(c.state.nodes))
	for _, node := range c.state.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Address < nodes[j].Address })
	latest := make([]wukongimport.MetricSampleSummary, 0, len(c.state.latest))
	for _, sample := range c.state.latest {
		latest = append(latest, sample)
	}
	sort.Slice(latest, func(i, j int) bool {
		if latest[i].Name == latest[j].Name {
			return labelsKey(latest[i].Labels) < labelsKey(latest[j].Labels)
		}
		return latest[i].Name < latest[j].Name
	})
	return wukongimport.MetricsSummary{
		ScrapeTicks: c.state.ticks, SelectedSamples: c.state.selectedSamples,
		DroppedMetricNames: c.state.droppedNames, Nodes: nodes,
		LatencyP95MS: percentile(c.state.latencies, 95), LatencyP99MS: percentile(c.state.latencies, 99),
		Latest: latest,
	}
}

func labelsKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(labels[key])
		out.WriteByte(',')
	}
	return out.String()
}

func percentile(values []float64, percent int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := (percent*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
```

- [ ] **Step 7: Verify worker behavior**

Run:

```bash
GOWORK=off go test ./units/wukongim/metrics_collector -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add units/wukongim/metrics_collector/unit.go units/wukongim/metrics_collector/collector.go units/wukongim/metrics_collector/unit_test.go
git commit -m "feat: collect wukongim metrics in background"
```

---

### Task 8: Register Collector and Add Example Scenario

**Files:**
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`
- Create: `examples/wukongim-send-rate-with-metrics.yaml`

- [ ] **Step 1: Write CLI registration tests**

In `cmd/wkbench/main_test.go`, add `"wukongim.metrics_collector/v1"` to `TestListUnitsIncludesWuKongIMBlackBoxUnits`.

Add this validation test:

```go
func TestValidateCommandAcceptsMetricsCollectorScenario(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: metrics-collector
  duration: 1ms
  report_dir: ./reports/test-metrics
units:
  target:
    use: wukongim.target
    spec:
      api_addrs:
        - http://127.0.0.1:5011
      gateway_tcp_addrs:
        - 127.0.0.1:5111
      bench_api_token: ""
      operation_timeout: 1s
  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: 1s
      timeout: 800ms
      include:
        - "wk_.*"
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"validate", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
}
```

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestListUnitsIncludesWuKongIMBlackBoxUnits|TestValidateCommandAcceptsMetricsCollectorScenario' -count=1
```

Expected: FAIL because the default registry does not register the collector.

- [ ] **Step 3: Register the unit**

In `cmd/wkbench/main.go`, add import:

```go
metricscollector "github.com/WuKongIM/wkbench/units/wukongim/metrics_collector"
```

In `defaultRegistry`, after `wukongtarget.Register(reg)` or before other WuKongIM units:

```go
metricscollector.Register(reg)
```

- [ ] **Step 4: Add example scenario**

Create `examples/wukongim-send-rate-with-metrics.yaml`:

```yaml
version: wkbench/v2

run:
  id: send-rate-with-metrics
  duration: 30s
  report_dir: ./reports/send-rate-with-metrics

units:
  target:
    use: wukongim.target
    spec:
      api_addrs:
        - http://127.0.0.1:5011
        - http://127.0.0.1:5012
        - http://127.0.0.1:5013
      gateway_tcp_addrs:
        - 127.0.0.1:5111
        - 127.0.0.1:5112
        - 127.0.0.1:5113
      bench_api_token: ""
      operation_timeout: 5s

  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: 1s
      timeout: 800ms
      path: /metrics
      include:
        - "wk_.*"
        - "wukongim_.*"
      exclude:
        - "go_.*"
        - "process_.*"
      fail_on_scrape_error: false
      max_summary_metrics: 100

  identities:
    use: identity.pool
    after: [metrics]
    spec:
      total: 1000
      uid_prefix: metrics-u
      device_prefix: metrics-d
      token_prefix: metrics-token

  tokens:
    use: wukongim.prepare_tokens

  pairs:
    use: identity.person_pairs
    spec:
      count: 500
      mode: ring
      bidirectional: true

  sessions:
    use: wkproto.session_pool
    after: [tokens]
    spec:
      connect_rate: 100/s

  traffic:
    use: traffic.send
    after: [metrics]
    inputs:
      targets: pairs.targets
      sender: sessions.message_sender
    spec:
      rate: 1000/s
      payload_size: 128
      sender_pick: round_robin
      max_in_flight: 1000
      ack_timeout: 5s
```

- [ ] **Step 5: Verify CLI and example**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestListUnitsIncludesWuKongIMBlackBoxUnits|TestValidateCommandAcceptsMetricsCollectorScenario' -count=1
GOWORK=off go run ./cmd/wkbench validate -scenario examples/wukongim-send-rate-with-metrics.yaml
```

Expected: PASS and `wkbench scenario is valid`.

- [ ] **Step 6: Commit**

```bash
git add cmd/wkbench/main.go cmd/wkbench/main_test.go examples/wukongim-send-rate-with-metrics.yaml
git commit -m "feat: register wukongim metrics collector"
```

---

### Task 9: Integrate Metrics Collection Into Send Rate Sweep Script

**Files:**
- Modify: `scripts/bench-wukongim-three-node-send-rate-sweep.sh`
- Modify: `scripts/smoke_test.go`

- [ ] **Step 1: Write dry-run rendering test**

Add to `scripts/smoke_test.go`:

```go
func TestSendRateSweepDryRunRendersMetricsCollector(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	outDir, _ := runSweepDryRun(t,
		"--mode", "person",
		"--collect-metrics",
		"--metrics-interval", "2s",
		"--metrics-include", "wk_.*,wukongim_.*",
		"--metrics-exclude", "go_.*",
	)
	data, err := os.ReadFile(filepath.Join(outDir, "steps", "0001-100qps", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"metrics:",
		"use: wukongim.metrics_collector",
		"after: [target]",
		"target: target.target",
		"interval: 2s",
		`- "wk_.*"`,
		`- "wukongim_.*"`,
		`- "go_.*"`,
		"after: [metrics]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("scenario missing %q:\n%s", want, text)
		}
	}
}

func TestSendRateSweepMixedDryRunRendersCollectorPerSubScenario(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	outDir, _ := runSweepDryRun(t,
		"--mode", "mixed",
		"--collect-metrics",
	)
	for _, scenario := range []string{
		filepath.Join(outDir, "steps", "0001-100qps", "group", "scenario.yaml"),
		filepath.Join(outDir, "steps", "0001-100qps", "person", "scenario.yaml"),
	} {
		data, err := os.ReadFile(scenario)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "use: wukongim.metrics_collector") {
			t.Fatalf("mixed sub-scenario missing collector:\n%s", data)
		}
	}
}
```

- [ ] **Step 2: Run script tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestSendRateSweepDryRunRendersMetricsCollector|TestSendRateSweepMixedDryRunRendersCollectorPerSubScenario' -count=1
```

Expected: FAIL because the script does not accept metrics flags and does not render the collector.

- [ ] **Step 3: Add script flags and usage text**

In `scripts/bench-wukongim-three-node-send-rate-sweep.sh`, add defaults near other top-level variables:

```bash
COLLECT_METRICS=0
METRICS_INTERVAL="1s"
METRICS_INCLUDE="wk_.*,wukongim_.*"
METRICS_EXCLUDE="go_.*,process_.*"
```

Add to `usage()`:

```text
  --collect-metrics
  --metrics-interval D
  --metrics-include REGEX_LIST
  --metrics-exclude REGEX_LIST
```

Add option parsing cases:

```bash
    --collect-metrics)
      COLLECT_METRICS=1
      shift
      ;;
    --metrics-interval)
      [[ $# -ge 2 ]] || die "--metrics-interval requires a value"
      METRICS_INTERVAL="$2"
      shift 2
      ;;
    --metrics-include)
      [[ $# -ge 2 ]] || die "--metrics-include requires a value"
      METRICS_INCLUDE="$2"
      shift 2
      ;;
    --metrics-exclude)
      [[ $# -ge 2 ]] || die "--metrics-exclude requires a value"
      METRICS_EXCLUDE="$2"
      shift 2
      ;;
```

- [ ] **Step 4: Add YAML list renderer and metrics unit renderer**

Add helpers after `render_common_prefix`:

```bash
render_regex_list() {
  local raw="$1"
  local item
  IFS=',' read -r -a items <<< "$raw"
  for item in "${items[@]}"; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    [[ -n "$item" ]] || continue
    printf '        - "%s"\n' "$item"
  done
}

render_metrics_collector() {
  if [[ "$COLLECT_METRICS" -eq 0 ]]; then
    return
  fi
  cat <<YAML
  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: $METRICS_INTERVAL
      timeout: 800ms
      path: /metrics
      include:
YAML
  render_regex_list "$METRICS_INCLUDE"
  cat <<YAML
      exclude:
YAML
  render_regex_list "$METRICS_EXCLUDE"
  cat <<YAML
      fail_on_scrape_error: false

YAML
}

```

Call `render_metrics_collector` immediately after `render_common_prefix` in `render_scenario`.

- [ ] **Step 5: Ensure first foreground setup waits for metrics**

In `render_scenario`, pass an additional `after` dependency to the first foreground setup. The concrete change is:

```bash
render_identity_after() {
  if [[ "$COLLECT_METRICS" -eq 1 ]]; then
    cat <<YAML
    after: [metrics]
YAML
  fi
}
```

Change the `identities` block in `render_common_prefix` from:

```yaml
  identities:
    use: identity.pool
    spec:
```

to:

```bash
  identities:
    use: identity.pool
$(render_identity_after)
    spec:
```

Keep traffic units with `after: [metrics]` when `COLLECT_METRICS=1` by adding a rendered line before each traffic `inputs:` block:

```bash
render_traffic_after() {
  if [[ "$COLLECT_METRICS" -eq 1 ]]; then
    cat <<YAML
    after: [metrics]
YAML
  fi
}
```

Use `$(render_traffic_after)` in both `render_person_traffic` and `render_group_traffic` directly under `use: traffic.send`.

- [ ] **Step 6: Verify script integration**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestSendRateSweepDryRunRendersMetricsCollector|TestSendRateSweepMixedDryRunRendersCollectorPerSubScenario|TestSendRateSweepDryRunDoesNotRequireJQ' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add scripts/bench-wukongim-three-node-send-rate-sweep.sh scripts/smoke_test.go
git commit -m "feat: collect metrics during send rate sweeps"
```

---

### Task 10: Final Verification and Real Smoke

**Files:**
- Review all changed files from Tasks 1-9.

- [ ] **Step 1: Run focused test groups**

Run:

```bash
GOWORK=off go test ./benchkit/contract ./benchkit/kernel ./benchkit/report ./benchkit/ports/wukongim ./units/wukongim/metrics_collector ./cmd/wkbench ./scripts -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full test suite**

Run:

```bash
GOWORK=off go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Validate example scenario**

Run:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario examples/wukongim-send-rate-with-metrics.yaml
```

Expected output contains:

```text
wkbench scenario is valid
```

- [ ] **Step 4: Dry-run send rate sweep with metrics**

Run:

```bash
scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 20 \
  --duration 3s \
  --users 100 \
  --person-pairs 50 \
  --groups 10 \
  --members 10 \
  --collect-metrics \
  --metrics-interval 1s \
  --dry-run \
  --no-start-target \
  --out-dir ./reports/send-rate-sweep/metrics-dry-run
```

Expected:

- `reports/send-rate-sweep/metrics-dry-run/steps/0001-20qps/group/scenario.yaml` contains `use: wukongim.metrics_collector`.
- `reports/send-rate-sweep/metrics-dry-run/steps/0001-20qps/person/scenario.yaml` contains `use: wukongim.metrics_collector`.
- `reports/send-rate-sweep/metrics-dry-run/summary.md` still contains `latency_p95` and `latency_p99` columns.

- [ ] **Step 5: Real low-QPS smoke against local three-node target**

Run only when the local WuKongIM build is available:

```bash
scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 20 \
  --duration 3s \
  --users 100 \
  --person-pairs 50 \
  --groups 10 \
  --members 10 \
  --collect-metrics \
  --metrics-interval 1s \
  --start-target \
  --clean-target \
  --out-dir ./reports/send-rate-sweep/metrics-smoke
```

Expected:

- Overall script exits `0`.
- Each successful sub-report contains `units.metrics.artifacts.metrics.jsonl.path`.
- `metrics.jsonl` exists under each report's `artifacts/metrics/` directory.
- `summary.md` includes the metrics unit with `scrape_latency` p95/p99 in milliseconds.

- [ ] **Step 6: Inspect result JSON fields**

Run:

```bash
jq '.units.metrics | {status, elapsed_ms, artifacts, metrics, summary: .outputs.summary.value}' \
  ./reports/send-rate-sweep/metrics-smoke/steps/0001-20qps/person/report.json
```

Expected:

- `status` is `"completed"`.
- `elapsed_ms` is at least the foreground workload duration in milliseconds.
- `artifacts["metrics.jsonl"].path` is present.
- `.metrics.scrape_latency.p95` and `.metrics.scrape_latency.p99` are numbers in seconds in JSON; Markdown renders them as milliseconds.

- [ ] **Step 7: Commit final cleanups if needed**

If any verification-only adjustment was required, commit with:

```bash
git add <changed-files>
git commit -m "test: verify background metrics collection"
```

Skip this commit when Tasks 1-9 already pass without additional changes.

---

## Spec Coverage Mapping

- Background lifecycle interfaces and graph execution: Tasks 2 and 3.
- Existing units remaining unchanged: Tasks 2 and 3 because `BackgroundUnit` is optional and normal `Run` behavior stays covered.
- Timing fields in milliseconds: Task 1 and report assertion in Task 1.
- Artifact creation and metadata: Task 4.
- WuKongIM metrics collector input, output, artifact, spec, parser, and scrape behavior: Tasks 5, 6, and 7.
- Collector health metrics and p95/p99 scrape latency: Tasks 5 and 7.
- Summary bounded by `max_summary_metrics`: Task 7.
- Report JSON and Markdown inclusion without inlining raw samples: Tasks 4 and 5.
- Example scenario and send-rate sweep integration: Tasks 8 and 9.
- Dry-run and real smoke verification: Task 10.

## Self-Review Checklist

- [ ] Every new public interface is introduced before any task uses it.
- [ ] Every created or modified file is listed in the File Structure section.
- [ ] Every behavior from `docs/superpowers/specs/2026-06-02-wkbench-background-units-design.md` maps to at least one task above.
- [ ] All commands use `GOWORK=off` for Go invocations.
- [ ] Kernel duration metrics remain stored in seconds, while reports and sweep-facing summaries render milliseconds.
- [ ] No raw metrics samples are inlined into `summary.md`; raw samples stay in `metrics.jsonl`.
- [ ] Background units are stopped in reverse start order in both success and failure paths.
- [ ] The send-rate sweep dry-run remains independent of `jq`.
