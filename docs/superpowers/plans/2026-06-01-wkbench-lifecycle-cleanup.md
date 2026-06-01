# wkbench Lifecycle Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure runtime resources produced by units are closed after scenario execution and cleanup problems are visible in reports without changing the run success status.

**Architecture:** Add a tiny optional close contract to `benchkit/contract`, teach the kernel to close output values in reverse unit execution order, and record cleanup results on the producing unit. Reports render cleanup errors beside unit outputs. Units stay independent; resource ownership belongs to the output value that implements the close contract.

**Tech Stack:** Go standard library, existing `benchkit/contract`, `benchkit/kernel`, and `benchkit/report`.

---

### Task 1: Cleanup Result Model And Report Rendering

**Files:**
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/report/report.go`
- Modify: `benchkit/report/report_test.go`

- [ ] **Step 1: Write failing report test**

Add a test to `benchkit/report/report_test.go`:

```go
func TestWriteDirIncludesCleanupErrors(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "demo",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"sessions": {
				Kind:   "wkproto.session_pool/v1",
				Status: kernel.StatusCompleted,
				Cleanup: []kernel.CleanupResult{
					{Output: "group_sender", Error: "close failed"},
				},
			},
		},
	}
	if err := report.WriteDir(dir, result); err != nil {
		t.Fatalf("write report: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "cleanup `group_sender`: close failed") {
		t.Fatalf("summary.md missing cleanup error:\n%s", text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./benchkit/report`

Expected: FAIL because `kernel.CleanupResult` and `UnitResult.Cleanup` do not exist.

- [ ] **Step 3: Add result types and Markdown rendering**

Add to `benchkit/kernel/kernel.go`:

```go
type CleanupResult struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}
```

Add `Cleanup []CleanupResult` to `UnitResult`. Update `benchkit/report/report.go` to render each cleanup entry under the unit as:

```markdown
  - cleanup `group_sender`: close failed
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./benchkit/report`

Expected: PASS.

### Task 2: Kernel Output Cleanup

**Files:**
- Modify: `benchkit/contract/types.go`
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`

- [ ] **Step 1: Write failing kernel tests**

Add tests to `benchkit/kernel/kernel_test.go` that prove:

- closeable outputs are closed after a successful run,
- cleanup happens in reverse unit execution order,
- cleanup errors are recorded while `Result.Status` remains `completed`,
- already executed outputs are closed when a later unit fails.

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./benchkit/kernel`

Expected: FAIL because kernel does not close outputs.

- [ ] **Step 3: Add optional close contract**

Add to `benchkit/contract/types.go`:

```go
// CloseableOutput is implemented by output values that own runtime resources.
type CloseableOutput interface {
	Close() error
}
```

- [ ] **Step 4: Implement cleanup in kernel**

Update `Engine.Run` so it always attempts cleanup for outputs produced before return. Cleanup should:

- run after all units complete or immediately before returning a run error,
- traverse units in reverse `graph.order`,
- close only values implementing `contract.CloseableOutput`,
- record close errors in `result.Units[unit].Cleanup`,
- preserve the main run status and returned error.

- [ ] **Step 5: Run tests to verify they pass**

Run: `GOWORK=off go test ./benchkit/kernel`

Expected: PASS.

### Task 3: Documentation

**Files:**
- Modify: `docs/unit-standard.md`

- [ ] **Step 1: Document lifecycle ownership**

Add a section explaining that output values may implement `contract.CloseableOutput`, that kernel closes them after the scenario, and that cleanup errors appear in the report but do not turn a successful run into a failed run.

- [ ] **Step 2: Run docs-adjacent checks**

Run: `GOWORK=off go test ./...`

Expected: PASS.

### Task 4: Final Verification And Commit

**Files:**
- No new files unless verification exposes a real issue.

- [ ] **Step 1: Format**

Run:

```bash
gofmt -w benchkit/contract/types.go benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go benchkit/report/report.go benchkit/report/report_test.go
```

- [ ] **Step 2: Full tests**

Run: `GOWORK=off go test ./...`

Expected: PASS.

- [ ] **Step 3: Scenario smoke**

Run:

```bash
rm -rf ./reports/group-send-demo
GOWORK=off go run ./cmd/wkbench run -scenario ./examples/group-send.yaml
test -f ./reports/group-send-demo/summary.md
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add benchkit/contract/types.go benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go benchkit/report/report.go benchkit/report/report_test.go docs/unit-standard.md docs/superpowers/plans/2026-06-01-wkbench-lifecycle-cleanup.md
git commit -m "feat: add unit output lifecycle cleanup"
```
