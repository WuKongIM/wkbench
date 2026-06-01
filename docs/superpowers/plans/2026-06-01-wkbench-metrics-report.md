# wkbench Metrics Report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve metrics emitted by units in `kernel.Result` and render them in `report.json` and `summary.md`.

**Architecture:** Keep the unit contract unchanged. Add a process-local metric accumulator to `benchkit/kernel` through `runEnv`, copy aggregates into `UnitResult`, and let `benchkit/report` render the result model. This keeps units independent and keeps reporting outside unit packages.

**Tech Stack:** Go standard library, existing `benchkit/contract`, `benchkit/kernel`, `benchkit/report`, and `cmd/wkbench`.

---

### Task 1: Kernel Metric Result Model And Aggregation

**Files:**
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`

- [ ] **Step 1: Write failing kernel metric tests**

Add the following tests to `benchkit/kernel/kernel_test.go` after `TestEngineRecordsReportableOutputs`:

```go
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
```

Add the helper units near the other test units in the same file:

```go
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
```

Update imports in `benchkit/kernel/kernel_test.go` to include `math` and `time`.

- [ ] **Step 2: Run kernel tests to verify RED**

Run:

```bash
GOWORK=off go test ./benchkit/kernel
```

Expected: FAIL because `kernel.UnitResult` has no `Metrics` field and `kernel.MetricResult` does not exist.

- [ ] **Step 3: Add metric result types**

In `benchkit/kernel/kernel.go`, add `Metrics` to `UnitResult` after `Outputs`:

```go
// Metrics lists aggregated metrics emitted by the unit.
Metrics map[string]MetricResult `json:"metrics,omitempty"`
```

Add `MetricResult` after `OutputResult`:

```go
// MetricResult summarizes one aggregated unit metric.
type MetricResult struct {
	// Type is the metric kind, for example counter or duration.
	Type string `json:"type"`
	// Labels are the metric dimensions for labelled emissions.
	Labels contract.Labels `json:"labels,omitempty"`
	// Count is the number of emitted samples.
	Count int64 `json:"count"`
	// Sum is the accumulated value. Durations are recorded in seconds.
	Sum float64 `json:"sum"`
	// Min is the minimum observed value for duration metrics.
	Min float64 `json:"min,omitempty"`
	// Max is the maximum observed value for duration metrics.
	Max float64 `json:"max,omitempty"`
}
```

- [ ] **Step 4: Add a metric accumulator**

In `benchkit/kernel/kernel.go`, replace the `counters map[string]float64` field on `runEnv` with:

```go
metrics *metricStore
```

Add the following helper types and functions near `runEnv`:

```go
type metricStore struct {
	mu    sync.Mutex
	types map[string]string
	items map[string]MetricResult
}

func newMetricStore(defs []contract.MetricDef) *metricStore {
	types := make(map[string]string, len(defs))
	for _, def := range defs {
		if strings.TrimSpace(def.Name) == "" {
			continue
		}
		types[def.Name] = strings.TrimSpace(def.Type)
	}
	return &metricStore{types: types, items: make(map[string]MetricResult)}
}

func (s *metricStore) addCounter(name string, delta float64, labels contract.Labels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := metricKey(name, labels)
	item := s.items[key]
	if item.Type == "" {
		item.Type = metricType(s.types[name], "counter")
		item.Labels = cloneLabels(labels)
	}
	item.Count++
	item.Sum += delta
	s.items[key] = item
}

func (s *metricStore) observeDuration(name string, value time.Duration, labels contract.Labels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seconds := value.Seconds()
	key := metricKey(name, labels)
	item := s.items[key]
	if item.Type == "" {
		item.Type = metricType(s.types[name], "duration")
		item.Labels = cloneLabels(labels)
		item.Min = seconds
		item.Max = seconds
	} else {
		if seconds < item.Min {
			item.Min = seconds
		}
		if seconds > item.Max {
			item.Max = seconds
		}
	}
	item.Count++
	item.Sum += seconds
	s.items[key] = item
}

func (s *metricStore) results() map[string]MetricResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return nil
	}
	results := make(map[string]MetricResult, len(s.items))
	for key, item := range s.items {
		results[key] = item
	}
	return results
}

func metricType(declared string, fallback string) string {
	if strings.TrimSpace(declared) == "" {
		return fallback
	}
	return declared
}

func metricKey(name string, labels contract.Labels) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

func cloneLabels(labels contract.Labels) contract.Labels {
	if len(labels) == 0 {
		return nil
	}
	clone := make(contract.Labels, len(labels))
	for key, value := range labels {
		clone[key] = value
	}
	return clone
}
```

- [ ] **Step 5: Wire runEnv emissions into metrics**

In `Engine.Run`, construct the environment with metric definitions:

```go
env := &runEnv{
	baseEnv:  base,
	graph:    graph,
	unitName: name,
	outputs:  outputs,
	metrics:  newMetricStore(node.def.Metrics),
}
```

Update `EmitCounter` and `ObserveDuration`:

```go
func (e *runEnv) EmitCounter(name string, delta float64, labels contract.Labels) {
	e.metrics.addCounter(name, delta, labels)
}

func (e *runEnv) ObserveDuration(name string, value time.Duration, labels contract.Labels) {
	e.metrics.observeDuration(name, value, labels)
}
```

When a unit run fails, preserve metrics:

```go
result.Units[name] = UnitResult{
	Kind:    node.def.Kind,
	Status:  StatusWorkerFailed,
	Error:   err.Error(),
	Metrics: env.metrics.results(),
}
```

When a unit completes, include metrics next to outputs:

```go
result.Units[name] = UnitResult{
	Kind:    node.def.Kind,
	Status:  StatusCompleted,
	Outputs: outputs.resultsForUnit(name, node.def.Outputs),
	Metrics: env.metrics.results(),
}
```

- [ ] **Step 6: Run kernel tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./benchkit/kernel
```

Expected: PASS.

### Task 2: Report JSON And Markdown Metrics Rendering

**Files:**
- Modify: `benchkit/report/report.go`
- Modify: `benchkit/report/report_test.go`

- [ ] **Step 1: Write failing report tests**

Add the following test to `benchkit/report/report_test.go` after `TestWriteDirIncludesTrafficSummary`:

```go
func TestWriteDirIncludesMetrics(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "demo",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"traffic": {
				Kind:   "traffic.group_send/v1",
				Status: kernel.StatusCompleted,
				Metrics: map[string]kernel.MetricResult{
					"send_attempt_total": {
						Type:  "counter",
						Count: 2,
						Sum:   3,
					},
					"sendack_latency": {
						Type:  "duration",
						Count: 2,
						Sum:   0.003,
						Min:   0.001,
						Max:   0.002,
					},
				},
			},
		},
	}
	if err := report.WriteDir(dir, result); err != nil {
		t.Fatalf("write report: %v", err)
	}
	jsonData, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	jsonText := string(jsonData)
	for _, want := range []string{`"metrics"`, `"send_attempt_total"`, `"sendack_latency"`} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("report.json missing %q:\n%s", want, jsonText)
		}
	}
	markdownData, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	markdown := string(markdownData)
	for _, want := range []string{
		"metric `send_attempt_total` `counter`: count `2`, sum `3`",
		"metric `sendack_latency` `duration`: count `2`, avg `0.0015s`, min `0.0010s`, max `0.0020s`",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("summary.md missing %q:\n%s", want, markdown)
		}
	}
}
```

- [ ] **Step 2: Run report tests to verify RED**

Run:

```bash
GOWORK=off go test ./benchkit/report
```

Expected: FAIL because `summary.md` does not render metrics yet.

- [ ] **Step 3: Render metrics in summary.md**

In `benchkit/report/report.go`, add `strconv` to imports.

Inside `summaryMarkdown`, after the output loop and before the cleanup loop, add:

```go
metricNames := make([]string, 0, len(unit.Metrics))
for metricName := range unit.Metrics {
	metricNames = append(metricNames, metricName)
}
sort.Strings(metricNames)
for _, metricName := range metricNames {
	out += formatMetric(metricName, unit.Metrics[metricName])
}
```

Add these helpers after `formatOutputValue`:

```go
func formatMetric(name string, metric kernel.MetricResult) string {
	switch metric.Type {
	case "duration":
		avg := 0.0
		if metric.Count > 0 {
			avg = metric.Sum / float64(metric.Count)
		}
		return fmt.Sprintf(
			"  - metric `%s` `duration`: count `%d`, avg `%s`, min `%s`, max `%s`\n",
			name,
			metric.Count,
			formatSeconds(avg),
			formatSeconds(metric.Min),
			formatSeconds(metric.Max),
		)
	default:
		metricType := metric.Type
		if metricType == "" {
			metricType = "counter"
		}
		return fmt.Sprintf(
			"  - metric `%s` `%s`: count `%d`, sum `%s`\n",
			name,
			metricType,
			metric.Count,
			formatNumber(metric.Sum),
		)
	}
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatSeconds(value float64) string {
	return fmt.Sprintf("%.4fs", value)
}
```

- [ ] **Step 4: Run report tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./benchkit/report
```

Expected: PASS.

### Task 3: Traffic Metric Definition Alignment

**Files:**
- Modify: `units/traffic/group_send/unit.go`
- Modify: `units/traffic/group_send/unit_test.go`

- [ ] **Step 1: Write failing metric definition test**

Add the following test to `units/traffic/group_send/unit_test.go` after `TestGroupSendUsesPortsAndEmitsSummary`:

```go
func TestGroupSendDeclaresDurationMetric(t *testing.T) {
	def := groupsend.Unit{}.Definition()
	for _, metric := range def.Metrics {
		if metric.Name == "sendack_latency" {
			if metric.Type != "duration" {
				t.Fatalf("sendack_latency metric type = %q, want duration", metric.Type)
			}
			return
		}
	}
	t.Fatal("sendack_latency metric is not declared")
}
```

- [ ] **Step 2: Run traffic unit tests to verify RED**

Run:

```bash
GOWORK=off go test ./units/traffic/group_send
```

Expected: FAIL because `sendack_latency` is currently declared as `histogram`.

- [ ] **Step 3: Change latency metric type**

In `units/traffic/group_send/unit.go`, change the metric definition:

```go
{Name: "sendack_latency", Type: "duration"},
```

- [ ] **Step 4: Run traffic unit tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./units/traffic/group_send
```

Expected: PASS.

### Task 4: Documentation For Metric Reporting

**Files:**
- Modify: `docs/unit-standard.md`

- [ ] **Step 1: Update unit standard documentation**

In `docs/unit-standard.md`, after the reportable output section and before `Runtime Resource Cleanup`, add:

````markdown
## Metrics

Units emit metrics only through `RunEnv`:

```go
env.EmitCounter("send_attempt_total", 1, nil)
env.ObserveDuration("sendack_latency", latency, nil)
```

Declare metrics in `Definition` so `explain`, reports, and future planners can
describe the unit contract:

```go
Metrics: []contract.MetricDef{
	{Name: "send_attempt_total", Type: "counter"},
	{Name: "sendack_latency", Type: "duration"},
}
```

The kernel aggregates metrics per unit. Counters record emission count and
delta sum. Durations record count, sum, min, and max in seconds. Metrics are
written to `report.json` and rendered in `summary.md`.
````

- [ ] **Step 2: Run full tests after docs**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

### Task 5: Final Verification, Scenario Smoke, And Commit

**Files:**
- Modify only files already listed in Tasks 1-4.

- [ ] **Step 1: Format changed Go files**

Run:

```bash
gofmt -w benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go benchkit/report/report.go benchkit/report/report_test.go units/traffic/group_send/unit.go units/traffic/group_send/unit_test.go
```

- [ ] **Step 2: Run full test suite**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run dry scenario report smoke**

Run:

```bash
rm -rf ./reports/group-send-demo
GOWORK=off go run ./cmd/wkbench run -scenario ./examples/group-send.yaml
test -f ./reports/group-send-demo/report.json
test -f ./reports/group-send-demo/summary.md
rg -n "send_attempt_total|sendack_success_total|sendack_error_total|sendack_latency" ./reports/group-send-demo/report.json ./reports/group-send-demo/summary.md
```

Expected: all commands exit 0, and `rg` prints all four metric names at least once.

- [ ] **Step 4: Inspect git diff**

Run:

```bash
git diff --check
git status -sb
```

Expected: `git diff --check` exits 0, and status shows only the planned files modified or added.

- [ ] **Step 5: Commit implementation**

Run:

```bash
git add benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go benchkit/report/report.go benchkit/report/report_test.go units/traffic/group_send/unit.go units/traffic/group_send/unit_test.go docs/unit-standard.md docs/superpowers/plans/2026-06-01-wkbench-metrics-report.md
git commit -m "feat: add metrics reporting"
```
