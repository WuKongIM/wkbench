# Latency Percentiles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add p95 and p99 latency values to SEND -> SENDACK metrics, reports, and sweep summaries.

**Architecture:** Duration metrics will continue to store seconds internally. The kernel will compute p95 and p99 from recorded duration samples when building `MetricResult`; report and sweep layers will only format existing fields into milliseconds.

**Tech Stack:** Go metric aggregation and report tests, Bash sweep script with `jq` extraction, Go script smoke tests.

---

### Task 1: Kernel Duration Percentiles

**Files:**
- Modify: `benchkit/kernel/kernel.go`
- Test: `benchkit/kernel/kernel_test.go`

- [ ] **Step 1: Write the failing test**
  Add assertions in `TestEngineRecordsEmittedMetrics` that a duration metric with samples 1ms and 2ms exposes `P95 == 0.002` and `P99 == 0.002`.

- [ ] **Step 2: Run test to verify it fails**
  Run: `GOWORK=off go test ./benchkit/kernel -run TestEngineRecordsEmittedMetrics -count=1`
  Expected: FAIL because `MetricResult` has no percentile fields yet.

- [ ] **Step 3: Write minimal implementation**
  Add `P95` and `P99` fields to `MetricResult`, store duration samples per metric key in `metricStore`, and compute nearest-rank percentiles during `results()`.

- [ ] **Step 4: Run test to verify it passes**
  Run: `GOWORK=off go test ./benchkit/kernel -run TestEngineRecordsEmittedMetrics -count=1`
  Expected: PASS.

### Task 2: Report Percentile Formatting

**Files:**
- Modify: `benchkit/report/report.go`
- Test: `benchkit/report/report_test.go`

- [ ] **Step 1: Write the failing test**
  Extend `TestWriteDirIncludesMetrics` so `report.json` includes `"p95"` and `"p99"`, and `summary.md` includes `p95` and `p99` values in milliseconds.

- [ ] **Step 2: Run test to verify it fails**
  Run: `GOWORK=off go test ./benchkit/report -run TestWriteDirIncludesMetrics -count=1`
  Expected: FAIL because Markdown does not format p95/p99 yet.

- [ ] **Step 3: Write minimal implementation**
  Update duration metric Markdown to include `p95` and `p99` between `avg` and `min`.

- [ ] **Step 4: Run test to verify it passes**
  Run: `GOWORK=off go test ./benchkit/report -run TestWriteDirIncludesMetrics -count=1`
  Expected: PASS.

### Task 3: Sweep Percentile Columns

**Files:**
- Modify: `scripts/bench-wukongim-three-node-send-rate-sweep.sh`
- Test: `scripts/smoke_test.go`

- [ ] **Step 1: Write the failing test**
  Extend sweep script tests to require `.metrics.sendack_latency.p95`, `.metrics.sendack_latency.p99`, `latency_p95_ms`, `latency_p99_ms`, `latency_p95`, and `latency_p99`.

- [ ] **Step 2: Run test to verify it fails**
  Run: `GOWORK=off go test ./scripts -run TestSendRateSweepScriptExtractsReportJSONFields -count=1`
  Expected: FAIL because the sweep script only extracts avg/min/max.

- [ ] **Step 3: Write minimal implementation**
  Read p95/p99 from `report.json`, add CSV and Markdown columns, and use zero or `n/a` consistently for missing and total rows.

- [ ] **Step 4: Run test to verify it passes**
  Run: `GOWORK=off go test ./scripts -run 'TestSendRateSweepScriptExtractsReportJSONFields|TestSendRateSweepDryRunMixedHasAggregateAndNoZeroRates' -count=1`
  Expected: PASS.

### Task 4: Verification

**Files:**
- Verify all touched packages and dry-run generated scenarios.

- [ ] **Step 1: Run full tests**
  Run: `GOWORK=off go test ./...`
  Expected: PASS.

- [ ] **Step 2: Run mixed sweep dry-run**
  Run: `./scripts/bench-wukongim-three-node-send-rate-sweep.sh --mode mixed --rates 1,10 --duration 1s --dry-run --out-dir /tmp/wkbench-percentiles-dry`
  Expected: summary contains `latency_p95` and `latency_p99`, no `rate: 0/s`, generated group/person scenarios validate.
