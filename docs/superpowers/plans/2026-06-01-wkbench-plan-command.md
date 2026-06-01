# wkbench Plan Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a non-executing `wkbench plan` command that shows deterministic per-unit plans before running a benchmark.

**Architecture:** Reuse the existing kernel graph builder so plan, explain, validate, and run agree on unit resolution, auto-wiring, and execution order. `Engine.Plan` calls `Validate` and unit `Plan`, never `Run`; the CLI renders the result as text or JSON, while unit-specific plan details stay inside each unit's `contract.Plan`.

**Tech Stack:** Go standard library, existing `benchkit/kernel`, `benchkit/contract`, `benchkit/dsl`, `benchkit/registry`, and `cmd/wkbench` CLI pattern.

---

### Task 1: Kernel Plan Result

**Files:**
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`

- [x] **Step 1: Write failing kernel tests**

Add tests to `benchkit/kernel/kernel_test.go`:

```go
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
	if fmt.Sprint(calls) != "[validate:source plan:source validate:sink plan:sink]" {
		t.Fatalf("unexpected calls: %#v", calls)
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
	if fmt.Sprint(calls) != "[validate:probe plan]" {
		t.Fatalf("unexpected lifecycle calls got %v", calls)
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
```

Add local test units:

```go
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
	return contract.Plan{UnitName: env.UnitName(), Shards: []any{map[string]any{"name": env.UnitName()}}}, nil
}

func (u planningSourceUnit) Run(context.Context, contract.RunEnv) error {
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
```

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off go test ./benchkit/kernel
```

Expected: FAIL with `kernel.New(reg).Plan undefined`.

- [x] **Step 3: Implement kernel plan structs and method**

Add to `benchkit/kernel/kernel.go` near `Result` and `Explanation` types:

```go
type PlanResult struct {
	RunID  string                    `json:"run_id"`
	Status Status                    `json:"status"`
	Order  []string                  `json:"order"`
	Units  map[string]UnitPlanResult `json:"units"`
	Wiring []ExplainBinding          `json:"wiring,omitempty"`
}

type UnitPlanResult struct {
	Kind   string        `json:"kind"`
	Status Status        `json:"status"`
	Error  string        `json:"error,omitempty"`
	Plan   contract.Plan `json:"plan,omitempty"`
}
```

Add `Engine.Plan`:

```go
func (e *Engine) Plan(ctx context.Context, scenario dsl.Scenario) (PlanResult, error) {
	result := PlanResult{RunID: scenario.Run.ID, Status: StatusCompleted, Units: make(map[string]UnitPlanResult, len(scenario.Units))}
	graph, err := e.buildGraph(scenario)
	if err != nil {
		result.Status = StatusConfigFailed
		return result, err
	}
	result.Order = append([]string(nil), graph.order...)
	result.Wiring = graphWiring(graph)
	for _, name := range graph.order {
		node := graph.nodes[name]
		base := newBaseEnv(scenario, name, node.dsl.Spec)
		if err := node.unit.Validate(ctx, base); err != nil {
			result.Status = StatusConfigFailed
			result.Units[name] = UnitPlanResult{Kind: node.def.Kind, Status: StatusConfigFailed, Error: err.Error()}
			return result, fmt.Errorf("unit %q validate: %w", name, err)
		}
		plan, err := node.unit.Plan(ctx, base)
		if err != nil {
			result.Status = StatusPlanFailed
			result.Units[name] = UnitPlanResult{Kind: node.def.Kind, Status: StatusPlanFailed, Error: err.Error()}
			return result, fmt.Errorf("unit %q plan: %w", name, err)
		}
		result.Units[name] = UnitPlanResult{Kind: node.def.Kind, Status: StatusCompleted, Plan: plan}
	}
	return result, nil
}
```

Extract shared wiring construction:

```go
func graphWiring(graph *graph) []ExplainBinding {
	var wiring []ExplainBinding
	for _, name := range graph.order {
		node := graph.nodes[name]
		for _, input := range node.def.Inputs {
			ref, ok := node.bindings[input.Name]
			if !ok {
				continue
			}
			wiring = append(wiring, ExplainBinding{
				Unit:         name,
				Input:        input.Name,
				SourceUnit:   ref.unit,
				SourceOutput: ref.port,
				Type:         input.Type,
			})
		}
	}
	return wiring
}
```

Update `Explain` to use `graphWiring(graph)`.

- [x] **Step 4: Run test to verify it passes**

Run:

```bash
GOWORK=off go test ./benchkit/kernel
```

Expected: PASS.

### Task 2: CLI Plan Command

**Files:**
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`

- [x] **Step 1: Write failing CLI tests**

Add tests to `cmd/wkbench/main_test.go`:

```go
func TestPlanCommandPrintsScenarioPlan(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-plan
  duration: 1s
units:
  groups:
    use: core.static_groups
    spec:
      count: 1
      members_per_channel: 2
  sender:
    use: core.fake_group_sender
  traffic:
    use: traffic.group_send
    spec:
      rate: 2/s
      payload_size: 16
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plan", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"Run: cli-plan",
		"Execution Order:",
		"Plans:",
		"traffic: traffic.group_send/v1",
		"status: completed",
		"shards: 1",
		"Wiring:",
		"traffic.channels <- groups.groups",
		"traffic.sender <- sender.sender",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected plan output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestPlanCommandPrintsJSON(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-plan-json
  duration: 1s
units:
  groups:
    use: core.static_groups
    spec:
      count: 1
      members_per_channel: 2
  sender:
    use: core.fake_group_sender
  traffic:
    use: traffic.group_send
    spec:
      rate: 2/s
      payload_size: 16
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plan", "-scenario", scenarioPath, "-format", "json"}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	var result kernel.PlanResult
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal plan result: %v\n%s", err, stderr.String())
	}
	if result.RunID != "cli-plan-json" || result.Status != kernel.StatusCompleted {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Join(result.Order, ",") != "groups,sender,traffic" {
		t.Fatalf("unexpected order: %#v", result.Order)
	}
	if len(result.Units["traffic"].Plan.Shards) != 1 {
		t.Fatalf("unexpected traffic plan: %#v", result.Units["traffic"].Plan)
	}
}

func TestPlanCommandRejectsUnsupportedFormat(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-plan-format
units:
  groups:
    use: core.static_groups
    spec:
      count: 1
      members_per_channel: 2
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plan", "-scenario", scenarioPath, "-format", "yaml"}, &stderr)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unsupported plan format") {
		t.Fatalf("expected unsupported format error, got %q", stderr.String())
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off go test ./cmd/wkbench
```

Expected: FAIL because `plan` is unknown.

- [x] **Step 3: Implement CLI route and renderers**

Update usage:

```go
fmt.Fprintln(stderr, "usage: wkbench <list-units|new-unit|explain|plan|validate|run>")
```

Add switch route:

```go
case "plan":
	return runPlan(reg, args[1:], stderr)
```

Add `runPlan`:

```go
func runPlan(reg *registry.Registry, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var scenarioPath string
	var format string
	fs.StringVar(&scenarioPath, "scenario", "", "path to wkbench/v2 scenario yaml")
	fs.StringVar(&format, "format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if scenarioPath == "" {
		fmt.Fprintln(stderr, "-scenario is required")
		return exitConfig
	}
	if format != "text" && format != "json" {
		fmt.Fprintf(stderr, "unsupported plan format %q\n", format)
		return exitConfig
	}
	scenario, code := loadScenario(scenarioPath, stderr)
	if code != exitOK {
		return code
	}
	result, err := kernel.New(reg).Plan(context.Background(), scenario)
	if err != nil {
		fmt.Fprintf(stderr, "plan failed: %v\n", err)
		return exitConfig
	}
	switch format {
	case "json":
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "marshal plan failed: %v\n", err)
			return exitInternal
		}
		fmt.Fprintln(stderr, string(data))
	default:
		writePlanText(stderr, result)
	}
	return exitOK
}
```

Add `writePlanText`:

```go
func writePlanText(w io.Writer, result kernel.PlanResult) {
	fmt.Fprintf(w, "Run: %s\n", result.RunID)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Execution Order:")
	for i, name := range result.Order {
		unit := result.Units[name]
		if unit.Kind == "" {
			fmt.Fprintf(w, "  %d. %s\n", i+1, name)
			continue
		}
		fmt.Fprintf(w, "  %d. %s (%s)\n", i+1, name, unit.Kind)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Plans:")
	for _, name := range result.Order {
		unit := result.Units[name]
		fmt.Fprintf(w, "  %s: %s\n", name, unit.Kind)
		fmt.Fprintf(w, "    status: %s\n", unit.Status)
		if unit.Error != "" {
			fmt.Fprintf(w, "    error: %s\n", unit.Error)
		}
		if len(unit.Plan.Shards) > 0 {
			fmt.Fprintf(w, "    shards: %d\n", len(unit.Plan.Shards))
		}
	}
	fmt.Fprintln(w)
	writeWiringText(w, result.Wiring)
}
```

Extract wiring text from `writeExplainText`:

```go
func writeWiringText(w io.Writer, wiring []kernel.ExplainBinding) {
	fmt.Fprintln(w, "Wiring:")
	if len(wiring) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, binding := range wiring {
		fmt.Fprintf(w, "  %s.%s <- %s.%s (%s)\n",
			binding.Unit,
			binding.Input,
			binding.SourceUnit,
			binding.SourceOutput,
			binding.Type,
		)
	}
}
```

Update `writeExplainText` to call `writeWiringText(w, explanation.Wiring)`.

- [x] **Step 4: Run test to verify it passes**

Run:

```bash
GOWORK=off go test ./cmd/wkbench
```

Expected: PASS.

### Task 3: Group Send Plan Detail

**Files:**
- Modify: `units/traffic/group_send/unit.go`
- Modify: `units/traffic/group_send/unit_test.go`

- [x] **Step 1: Write failing unit test**

Add to `units/traffic/group_send/unit_test.go`:

```go
func TestGroupSendPlanReportsDeterministicShard(t *testing.T) {
	unit := groupsend.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", nil, map[string]any{
		"rate":          "2.5/s",
		"payload_size":  32,
		"sender_pick":   "round_robin",
		"max_in_flight": 8,
	})
	env.SetRunDuration(2 * time.Second)

	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	plan, err := unit.Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.UnitName != "traffic" {
		t.Fatalf("unexpected unit name %q", plan.UnitName)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("unexpected shards: %#v", plan.Shards)
	}
	data, err := json.Marshal(plan.Shards[0])
	if err != nil {
		t.Fatalf("marshal shard: %v", err)
	}
	var shard struct {
		TotalMessages int64   `json:"total_messages"`
		RatePerSecond float64 `json:"rate_per_second"`
		DurationMS    int64   `json:"duration_ms"`
		PayloadSize   int     `json:"payload_size"`
		SenderPick    string  `json:"sender_pick"`
		MaxInFlight   int     `json:"max_in_flight"`
	}
	if err := json.Unmarshal(data, &shard); err != nil {
		t.Fatalf("unmarshal shard: %v", err)
	}
	if shard.TotalMessages != 5 || shard.RatePerSecond != 2.5 || shard.DurationMS != 2000 || shard.PayloadSize != 32 || shard.SenderPick != "round_robin" || shard.MaxInFlight != 8 {
		t.Fatalf("unexpected shard: %#v", shard)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off go test ./units/traffic/group_send
```

Expected: FAIL because the plan has no shard details.

- [x] **Step 3: Implement `traffic.group_send` shard plan**

Add:

```go
type planShard struct {
	TotalMessages int64   `json:"total_messages"`
	RatePerSecond float64 `json:"rate_per_second"`
	DurationMS    int64   `json:"duration_ms"`
	PayloadSize   int     `json:"payload_size"`
	SenderPick    string  `json:"sender_pick,omitempty"`
	MaxInFlight   int     `json:"max_in_flight,omitempty"`
}
```

Update `Plan`:

```go
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	spec, err := decodeSpec(env)
	if err != nil {
		return contract.Plan{}, err
	}
	totalMessages := totalMessages(spec.Rate, env.RunDuration())
	return contract.Plan{
		UnitName: env.UnitName(),
		Shards: []any{
			planShard{
				TotalMessages: totalMessages,
				RatePerSecond: spec.Rate.PerSecond,
				DurationMS:    env.RunDuration().Milliseconds(),
				PayloadSize:   spec.PayloadSize,
				SenderPick:    spec.SenderPick,
				MaxInFlight:   spec.MaxInFlight,
			},
		},
	}, nil
}
```

Extract shared total calculation:

```go
func totalMessages(rate contract.Rate, duration time.Duration) int64 {
	total := int64(math.Round(rate.PerSecond * duration.Seconds()))
	if total < 1 {
		return 1
	}
	return total
}
```

Update `Run` to call `totalMessages(spec.Rate, env.RunDuration())`.

- [x] **Step 4: Run test to verify it passes**

Run:

```bash
GOWORK=off go test ./units/traffic/group_send
```

Expected: PASS.

### Task 4: Documentation And Final Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/scenario-dsl.md`
- Modify: `docs/superpowers/plans/2026-06-01-wkbench-plan-command.md`

- [x] **Step 1: Document plan usage**

Add near existing `explain` examples:

```bash
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml -format json
```

Document that `plan` validates the scenario and materializes unit `Plan` output without executing units, touching target services, creating outputs, or writing reports.

- [x] **Step 2: Format changed Go files**

Run:

```bash
gofmt -w benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go cmd/wkbench/main.go cmd/wkbench/main_test.go units/traffic/group_send/unit.go units/traffic/group_send/unit_test.go
```

Expected: command exits 0.

- [x] **Step 3: Run focused tests**

Run:

```bash
GOWORK=off go test ./benchkit/kernel ./cmd/wkbench ./units/traffic/group_send
```

Expected: PASS.

- [x] **Step 4: Run full tests**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [x] **Step 5: Run CLI smoke**

Run:

```bash
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml -format json
```

Expected: both commands exit 0, and the JSON output includes `"total_messages"` under `units.traffic.plan.shards`.

- [x] **Step 6: Commit**

Run:

```bash
git add README.md docs/scenario-dsl.md docs/superpowers/plans/2026-06-01-wkbench-plan-command.md benchkit/kernel cmd/wkbench units/traffic/group_send
git commit -m "feat: add scenario plan command"
```
