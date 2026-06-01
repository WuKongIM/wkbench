# wkbench Unit Authoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make third-party unit development repeatable by adding a unit scaffold command, reusable unit contract test helpers, an import-boundary test, and a concrete authoring guide.

**Architecture:** Keep authoring support outside runtime execution: `benchkit/unittest` holds test-only helpers, `benchkit/scaffold` owns file generation, and `cmd/wkbench` exposes the CLI command. Unit isolation remains enforced by a repository test that scans production imports under `units/**`.

**Tech Stack:** Go standard library, existing `benchkit/contract`, existing CLI test pattern, Markdown docs.

---

### Task 1: Unit Contract Test Helpers

**Files:**
- Create: `benchkit/unittest/unittest.go`
- Create: `benchkit/unittest/unittest_test.go`

- [ ] **Step 1: Write failing tests**

Create `benchkit/unittest/unittest_test.go` with tests for a valid unit, an invalid unversioned kind, and missing declared output.

```go
package unittest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
)

func TestAssertUnitContractAcceptsWellFormedDefinition(t *testing.T) {
	unittest.AssertUnitContract(t, goodUnit{})
}

func TestAssertUnitContractRejectsUnversionedKind(t *testing.T) {
	tb := &spyTB{}
	unittest.AssertUnitContract(tb, badKindUnit{})
	if !strings.Contains(tb.message, "kind must end with /vN") {
		t.Fatalf("expected versioned kind failure, got %q", tb.message)
	}
}

func TestAssertDeclaredOutputsRejectsMissingOutput(t *testing.T) {
	tb := &spyTB{}
	env := contract.NewTestRunEnv("run", "unit", nil, nil)
	unittest.AssertDeclaredOutputs(tb, outputUnit{}, env)
	if !strings.Contains(tb.message, `declared output "value" was not produced`) {
		t.Fatalf("expected missing output failure, got %q", tb.message)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./benchkit/unittest`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement helpers**

Implement `AssertUnitContract(t testing.TB, unit contract.Unit)` and `AssertDeclaredOutputs(t testing.TB, unit contract.Unit, outputs contract.OutputReader)`. The contract helper checks versioned kind suffix `/vN`, non-empty title and description, and non-empty unique input/output/metric/artifact names and types. The output helper checks that every declared output exists and that any `contract.ReportableOutput` value marshals to JSON.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./benchkit/unittest`

Expected: PASS.

### Task 2: Unit Import Boundary Test

**Files:**
- Create: `units/import_boundary_test.go`

- [ ] **Step 1: Write failing-capable boundary test**

Create a test that parses production Go files under `units/**` and fails when a file imports `github.com/WuKongIM/wkbench/units/...` unless that import path contains `/internal/`.

- [ ] **Step 2: Run test**

Run: `GOWORK=off go test ./units`

Expected: PASS on the current codebase because existing shared `benchapi` imports are under `units/wukongim/internal/benchapi`.

### Task 3: Scaffold Generator Package

**Files:**
- Create: `benchkit/scaffold/unit.go`
- Create: `benchkit/scaffold/unit_test.go`

- [ ] **Step 1: Write failing scaffold tests**

Add tests that call `scaffold.NewUnit` into a temp directory and assert it creates `unit.go`, `unit_test.go`, and `README.md` with the requested kind and `unittest.AssertUnitContract`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./benchkit/scaffold`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement generator**

Implement `UnitSpec`, `NewUnit`, package-name sanitization, title derivation from kind, and overwrite protection for generated files.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./benchkit/scaffold`

Expected: PASS.

### Task 4: CLI new-unit Command

**Files:**
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write failing CLI tests**

Add tests for:

```bash
wkbench new-unit -kind custom.echo/v1 -dir /tmp/.../units/custom/echo
```

The test asserts exit code 0, generated files, and a success message. Add a second test that pre-creates `unit.go` and expects exit code 1.

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./cmd/wkbench`

Expected: FAIL because `new-unit` is unknown.

- [ ] **Step 3: Wire CLI**

Add `new-unit` to usage and route it to `benchkit/scaffold.NewUnit` with flags `-kind`, `-dir`, `-package`, `-title`, and `-description`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./cmd/wkbench`

Expected: PASS.

### Task 5: Authoring Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/unit-standard.md`

- [ ] **Step 1: Update docs**

Document this concrete flow:

```bash
GOWORK=off go run ./cmd/wkbench new-unit -kind demo.group_send_probe/v1 -dir ./units/demo/group_send_probe
GOWORK=off go test ./units/demo/group_send_probe
```

Then show how to register the unit in a distribution build and compose it in scenario YAML. Emphasize that units do not import each other; composition happens through ports and DSL inputs.

- [ ] **Step 2: Run documentation-sensitive checks**

Run: `GOWORK=off go test ./...`

Expected: PASS.

### Task 6: Final Verification and Commit

**Files:**
- No new files unless verification exposes a real issue.

- [ ] **Step 1: Format**

Run: `gofmt -w benchkit/unittest/unittest.go benchkit/unittest/unittest_test.go benchkit/scaffold/unit.go benchkit/scaffold/unit_test.go units/import_boundary_test.go cmd/wkbench/main.go cmd/wkbench/main_test.go`

- [ ] **Step 2: Full tests**

Run: `GOWORK=off go test ./...`

Expected: PASS.

- [ ] **Step 3: CLI smoke**

Run:

```bash
tmp="$(mktemp -d)"
GOWORK=off go run ./cmd/wkbench new-unit -kind demo.echo/v1 -dir "$tmp/units/demo/echo"
test -f "$tmp/units/demo/echo/unit.go"
test -f "$tmp/units/demo/echo/unit_test.go"
test -f "$tmp/units/demo/echo/README.md"
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add README.md docs/unit-standard.md docs/superpowers/plans/2026-06-01-wkbench-unit-authoring.md benchkit/unittest benchkit/scaffold units/import_boundary_test.go cmd/wkbench
git commit -m "feat: add unit authoring scaffolds"
```
