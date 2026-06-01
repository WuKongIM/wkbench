# wkbench Explain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a non-executing scenario explanation command that shows execution order, unit contracts, and resolved input wiring.

**Architecture:** Reuse the existing kernel graph builder so `explain` sees the same auto-wiring and ordering as `validate` and `run`. `kernel.Explain` validates unit specs but never calls `Plan` or `Run`, and the CLI renders the explanation as deterministic text or JSON.

**Tech Stack:** Go standard library, existing `benchkit/kernel`, `benchkit/dsl`, `benchkit/registry`, and CLI command pattern.

---

### Task 1: Kernel Explanation Model

**Files:**
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`

- [x] **Step 1: Write failing kernel tests**

Add tests to `benchkit/kernel/kernel_test.go` that assert:

- `Explain` returns run id, execution order, unit kinds, outputs, and resolved wiring,
- auto-wired inputs are visible as `source.value`,
- `Explain` calls `Validate` but not `Plan` or `Run`.

- [x] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./benchkit/kernel`

Expected: FAIL because `Engine.Explain` does not exist.

- [x] **Step 3: Implement explanation structs and method**

Add:

```go
type Explanation struct {
	RunID string `json:"run_id"`
	Order []string `json:"order"`
	Units map[string]ExplainUnit `json:"units"`
	Wiring []ExplainBinding `json:"wiring,omitempty"`
}

type ExplainUnit struct {
	Kind string `json:"kind"`
	Inputs []ExplainPort `json:"inputs,omitempty"`
	Outputs []ExplainPort `json:"outputs,omitempty"`
	After []string `json:"after,omitempty"`
}

type ExplainPort struct {
	Name string `json:"name"`
	Type contract.PortType `json:"type"`
	Optional bool `json:"optional,omitempty"`
}

type ExplainBinding struct {
	Unit string `json:"unit"`
	Input string `json:"input"`
	SourceUnit string `json:"source_unit"`
	SourceOutput string `json:"source_output"`
	Type contract.PortType `json:"type"`
}
```

`Explain` should call `buildGraph`, call each unit's `Validate`, and construct deterministic order/wiring. It must not call `Plan` or `Run`.

- [x] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./benchkit/kernel`

Expected: PASS.

### Task 2: CLI Explain Command

**Files:**
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`

- [x] **Step 1: Write failing CLI tests**

Add tests for:

```bash
wkbench explain -scenario scenario.yaml
wkbench explain -scenario scenario.yaml -format json
```

Text output should contain `Execution Order`, `Wiring`, and `traffic.sender <- sender.sender`. JSON output should unmarshal and contain the expected `order` and `wiring`.

- [x] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./cmd/wkbench`

Expected: FAIL because `explain` is unknown.

- [x] **Step 3: Implement CLI route and renderers**

Add `explain` to usage and route to `runExplain`. It should parse `-scenario` and `-format`, call `kernel.New(reg).Explain`, then render:

- `text`: deterministic human-readable summary.
- `json`: `json.MarshalIndent` of `kernel.Explanation`.

Unsupported formats return `exitConfig`.

- [x] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./cmd/wkbench`

Expected: PASS.

### Task 3: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/scenario-dsl.md`

- [x] **Step 1: Document explain usage**

Add:

```bash
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-group-send.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-group-send.yaml -format json
```

Explain that this validates specs and wiring but does not execute units or touch target services.

- [x] **Step 2: Run docs-adjacent tests**

Run: `GOWORK=off go test ./...`

Expected: PASS.

### Task 4: Final Verification And Commit

**Files:**
- No new files unless verification exposes a real issue.

- [x] **Step 1: Format**

Run:

```bash
gofmt -w benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go cmd/wkbench/main.go cmd/wkbench/main_test.go
```

- [x] **Step 2: Full tests**

Run: `GOWORK=off go test ./...`

Expected: PASS.

- [x] **Step 3: CLI smoke**

Run:

```bash
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/group-send.yaml -format json
```

Expected: both commands exit 0.

- [x] **Step 4: Commit**

Run:

```bash
git add README.md docs/scenario-dsl.md docs/superpowers/plans/2026-06-01-wkbench-explain.md benchkit/kernel cmd/wkbench
git commit -m "feat: add scenario explain command"
```
