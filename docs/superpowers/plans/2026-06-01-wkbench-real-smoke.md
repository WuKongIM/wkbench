# wkbench Real Smoke Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a repeatable single-node WuKongIM smoke loop that runs the real black-box scenario and produces a report that shows SEND/SENDACK health directly.

**Architecture:** Keep this slice outside the unit dependency graph: the shell script orchestrates CLI commands, the kernel records only public reportable outputs, and the report package renders those outputs through stable port contracts. `wkproto.session_pool` remains independent from other units and only tightens its own request timeout and Sendack matching behavior.

**Tech Stack:** Go, Bash, YAML scenarios, existing `benchkit` contracts, WuKongIM public protocol packages.

---

### Task 1: Smoke Script

**Files:**
- Create: `scripts/smoke-wukongim-single-node.sh`
- Create: `scripts/smoke_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write the failing script test**

Add `scripts/smoke_test.go`:

```go
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSmokeScriptIsSyntaxCheckedAndRunsValidateBeforeRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash smoke script is for unix-like developer environments")
	}
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	script := filepath.Join(root, "scripts", "smoke-wukongim-single-node.sh")
	cmd := exec.Command("bash", "-n", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	validate := strings.Index(text, "go run ./cmd/wkbench validate -scenario")
	run := strings.Index(text, "go run ./cmd/wkbench run -scenario")
	if validate < 0 || run < 0 || validate > run {
		t.Fatalf("script must validate before run, got:\n%s", text)
	}
	if !strings.Contains(text, "WKBENCH_SCENARIO") {
		t.Fatalf("script should allow WKBENCH_SCENARIO override")
	}
}

func scriptPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return file
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./scripts`

Expected: FAIL because `scripts/smoke-wukongim-single-node.sh` does not exist.

- [ ] **Step 3: Write minimal smoke script**

Create `scripts/smoke-wukongim-single-node.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCENARIO="${WKBENCH_SCENARIO:-"$ROOT/examples/wukongim-group-send.yaml"}"

cd "$ROOT"

echo "wkbench smoke: validating $SCENARIO"
GOWORK=off go run ./cmd/wkbench validate -scenario "$SCENARIO"

echo "wkbench smoke: running $SCENARIO"
GOWORK=off go run ./cmd/wkbench run -scenario "$SCENARIO"
```

Run: `chmod +x scripts/smoke-wukongim-single-node.sh`

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./scripts`

Expected: PASS.

- [ ] **Step 5: Document usage**

Add to `README.md`:

````markdown
Run the single-node WuKongIM smoke after starting a target with bench API enabled:

```bash
./scripts/smoke-wukongim-single-node.sh
```

Override the scenario path when needed:

```bash
WKBENCH_SCENARIO=/path/to/scenario.yaml ./scripts/smoke-wukongim-single-node.sh
```
````

### Task 2: Report Traffic Summary Output

**Files:**
- Modify: `benchkit/contract/types.go`
- Modify: `benchkit/kernel/kernel.go`
- Modify: `benchkit/kernel/kernel_test.go`
- Modify: `benchkit/ports/traffic/summary.go`
- Modify: `benchkit/report/report.go`
- Modify: `benchkit/report/report_test.go`

- [ ] **Step 1: Write failing kernel and report tests**

In `benchkit/kernel/kernel_test.go`, add a unit that outputs a reportable value and assert `Result.Units["source"].Outputs["value"].Value` is present.

```go
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
```

In `benchkit/report/report_test.go`, assert the Markdown summary contains a readable SEND/SENDACK summary.

```go
func TestWriteDirIncludesTrafficSummary(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "demo",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"traffic": {
				Kind:   "traffic.group_send/v1",
				Status: kernel.StatusCompleted,
				Outputs: map[string]kernel.OutputResult{
					"summary": {
						Type:  traffic.SummaryV1,
						Value: traffic.Summary{SendackOK: 9, SendackErrors: 1, LastMessageID: 42},
					},
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
	for _, want := range []string{"sendack_ok: `9`", "sendack_errors: `1`", "sendack_error_rate: `0.1000`"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary.md missing %q:\n%s", want, text)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./benchkit/kernel ./benchkit/report`

Expected: FAIL because `kernel.OutputResult` and reportable output capture do not exist yet.

- [ ] **Step 3: Add reportable output contract and kernel capture**

Add to `benchkit/contract/types.go`:

```go
// ReportableOutput allows output values to opt into JSON/Markdown reports.
type ReportableOutput interface {
	// ReportOutput returns a JSON-friendly, non-sensitive summary value.
	ReportOutput() any
}
```

Add to `benchkit/kernel/kernel.go`:

```go
type OutputResult struct {
	Type  contract.PortType `json:"type"`
	Value any               `json:"value,omitempty"`
}
```

Change `UnitResult.Outputs` to `map[string]OutputResult` and populate it from `outputStore` only when the value implements `contract.ReportableOutput`.

- [ ] **Step 4: Make traffic summary reportable**

Add to `benchkit/ports/traffic/summary.go`:

```go
// ReportOutput implements contract.ReportableOutput.
func (s Summary) ReportOutput() any {
	return s
}
```

- [ ] **Step 5: Render traffic summary in Markdown**

Update `benchkit/report/report.go` to show reportable outputs under each unit and render `traffic.Summary` as:

```markdown
  - output `summary` `port.traffic.summary/v1`: sendack_ok: `9`, sendack_errors: `1`, sendack_error_rate: `0.1000`, last_message_id: `42`
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `GOWORK=off go test ./benchkit/kernel ./benchkit/report ./cmd/wkbench`

Expected: PASS.

### Task 3: WKProto Smoke Tightening

**Files:**
- Modify: `units/wkproto/session_pool/client.go`
- Create or modify: `units/wkproto/session_pool/client_test.go`

- [ ] **Step 1: Write failing Sendack matching and timeout tests**

Add `units/wkproto/session_pool/client_test.go` with package `sessionpool`:

```go
package sessionpool

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSendackMatchesClientMsgNoBeforeClientSeq(t *testing.T) {
	ack := &frame.SendackPacket{ClientSeq: 7, ClientMsgNo: "other"}
	if sendackMatchesRequest(ack, "expected", 7) {
		t.Fatal("must not match same sequence when request has a different client message number")
	}
	ack.ClientMsgNo = "expected"
	if !sendackMatchesRequest(ack, "expected", 8) {
		t.Fatal("should match client message number even when sequence differs")
	}
}

func TestWithRequestTimeoutPrefersRequestTimeout(t *testing.T) {
	client := &wkClient{operationTimeout: time.Hour}
	ctx, cancel := client.withRequestTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 500*time.Millisecond {
		t.Fatalf("expected request timeout deadline, remaining=%s", remaining)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./units/wkproto/session_pool`

Expected: FAIL because helpers do not exist and request timeout is not applied.

- [ ] **Step 3: Implement minimal helpers and wire them**

In `SendGroupAndWaitAck`, replace `withDefaultTimeout` with `withRequestTimeout(ctx, req.Timeout)`.

Add:

```go
func (c *wkClient) withRequestTimeout(ctx context.Context, requested time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	if requested > 0 {
		return context.WithTimeout(ctx, requested)
	}
	return c.withDefaultTimeout(ctx)
}

func sendackMatchesRequest(ack *frame.SendackPacket, clientMsgNo string, clientSeq uint64) bool {
	if ack == nil {
		return false
	}
	if clientMsgNo != "" {
		return ack.ClientMsgNo == clientMsgNo
	}
	return ack.ClientSeq == clientSeq
}
```

Use `sendackMatchesRequest` inside the Sendack read loop.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./units/wkproto/session_pool`

Expected: PASS.

### Task 4: Final Verification

**Files:**
- No new files unless verification exposes a real issue.

- [ ] **Step 1: Format**

Run: `gofmt -w benchkit/contract/types.go benchkit/kernel/kernel.go benchkit/kernel/kernel_test.go benchkit/ports/traffic/summary.go benchkit/report/report.go benchkit/report/report_test.go units/wkproto/session_pool/client.go units/wkproto/session_pool/client_test.go scripts/smoke_test.go`

- [ ] **Step 2: Full tests**

Run: `GOWORK=off go test ./...`

Expected: PASS.

- [ ] **Step 3: Validate real example**

Run: `GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-group-send.yaml`

Expected: `wkbench scenario is valid`.

- [ ] **Step 4: Run dry example and inspect report**

Run: `rm -rf ./reports/group-send-demo && GOWORK=off go run ./cmd/wkbench run -scenario ./examples/group-send.yaml`

Expected: PASS and `reports/group-send-demo/summary.md` contains `sendack_ok`.

- [ ] **Step 5: Commit**

Run:

```bash
git add README.md scripts benchkit units docs/superpowers/plans/2026-06-01-wkbench-real-smoke.md
git commit -m "feat: add real wukongim smoke loop"
```
