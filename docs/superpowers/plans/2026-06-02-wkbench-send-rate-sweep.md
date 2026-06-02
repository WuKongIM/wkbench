# Send Rate QPS Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a repeatable `wkbench` script that finds the highest passing `SEND -> SENDACK` QPS for person, group, or mixed traffic against the local WuKongIM v2 three-node target.

**Architecture:** Implement the sweep as a shell script in `scripts/` that generates temporary `wkbench/v2` scenarios for each QPS step, runs `wkbench validate` and `wkbench run`, extracts results from each step's `report.json` with `jq`, and writes aggregate `summary.md` and `summary.csv`. Mixed mode renders separate group/person sub-scenarios and runs them concurrently because the `wkbench` kernel executes units in graph order. Add fast Go tests around script syntax, dry-run scenario rendering, max-in-flight calculation, `jq` guard behavior, and command ordering.

**Tech Stack:** Bash, Go `testing`, `wkbench` CLI, `jq`, existing `traffic.send/v1`, existing three-node startup script.

---

## File Structure

- Create `scripts/bench-wukongim-three-node-send-rate-sweep.sh`
  - Parses sweep options.
  - Computes per-step person/group rates.
  - Computes per-workload `max_in_flight`.
  - Renders step `scenario.yaml` files. Mixed mode renders `group/scenario.yaml` and `person/scenario.yaml` per step.
  - Optionally starts/stops the local three-node target.
  - Runs `wkbench validate` before `wkbench run` for each step.
  - Extracts step results from `report.json`.
  - Writes `summary.md` and `summary.csv`.
- Modify `scripts/smoke_test.go`
  - Add script syntax test.
  - Add dry-run rendering tests for `person`, `group`, and `mixed`.
  - Add `jq` missing behavior test.
  - Add command ordering/content test.
- Modify `README.md`
  - Add concise examples for short smoke sweep and real highest-QPS sweep.

---

### Task 1: Add Sweep Script Skeleton And Guard Tests

**Files:**
- Create: `scripts/bench-wukongim-three-node-send-rate-sweep.sh`
- Modify: `scripts/smoke_test.go`

- [ ] **Step 1: Write failing tests for script presence, syntax, usage, and jq guard**

Add these helpers near the bottom of `scripts/smoke_test.go`, above `scriptPath`:

```go
func sweepScriptPath(t *testing.T) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	return filepath.Join(root, "scripts", "bench-wukongim-three-node-send-rate-sweep.sh")
}

func runBash(t *testing.T, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/bash", append([]string{script}, args...)...)
	cmd.Dir = filepath.Dir(filepath.Dir(scriptPath(t)))
	out, err := cmd.CombinedOutput()
	return string(out), err
}
```

Add this test:

```go
func TestSendRateSweepScriptSyntaxAndRequiredTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	script := sweepScriptPath(t)
	cmd := exec.Command("/bin/bash", "-n", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"command -v jq",
		"GOWORK=off go run ./cmd/wkbench validate -scenario",
		"GOWORK=off go run ./cmd/wkbench run -scenario",
		"console.txt",
		"summary.csv",
		"summary.md",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sweep script missing %q", want)
		}
	}
	validate := strings.Index(text, "go run ./cmd/wkbench validate -scenario")
	run := strings.Index(text, "go run ./cmd/wkbench run -scenario")
	if validate < 0 || run < 0 || validate > run {
		t.Fatalf("sweep script must validate before run")
	}
}
```

Add this test for missing `jq` in real-run mode:

```go
func TestSendRateSweepScriptRequiresJQForRealRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	script := sweepScriptPath(t)
	cmd := exec.Command("/bin/bash", script,
		"--mode", "person",
		"--rates", "1",
		"--duration", "1ms",
		"--out-dir", t.TempDir(),
		"--no-start-target",
	)
	cmd.Dir = filepath.Dir(filepath.Dir(scriptPath(t)))
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing jq failure, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "jq is required") {
		t.Fatalf("missing jq error should be clear, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestSendRateSweepScript' -count=1
```

Expected: FAIL because `scripts/bench-wukongim-three-node-send-rate-sweep.sh` does not exist.

- [ ] **Step 3: Add minimal script skeleton**

Create `scripts/bench-wukongim-three-node-send-rate-sweep.sh` with this executable skeleton:

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="mixed"
RATES="10,20"
DURATION="10s"
USERS=100
GROUPS=10
MEMBERS=10
PERSON_PAIRS=50
MIXED_RATIO="80:20"
PAYLOAD_SIZE=128
ACK_TIMEOUT="5s"
EXPECTED_LATENCY_MS=200
INFLIGHT_MULTIPLIER=2
MAX_IN_FLIGHT_CAP=20000
OUT_DIR="$ROOT/reports/send-rate-sweep/$(date +%Y%m%d-%H%M%S)"
START_TARGET=0
CLEAN_TARGET=0
KEEP_TARGET=0
DRY_RUN=0
TARGET_PID=""

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-three-node-send-rate-sweep.sh [options]

Find the highest passing WuKongIM SEND -> SENDACK QPS over an ordered rate list.

Options:
  --mode person|group|mixed
  --rates LIST
  --duration D
  --users N
  --groups N
  --members N
  --person-pairs N
  --mixed-ratio PERSON:GROUP
  --payload-size BYTES
  --ack-timeout D
  --expected-latency-ms N
  --inflight-multiplier N
  --max-in-flight-cap N
  --out-dir DIR
  --start-target
  --no-start-target
  --clean-target
  --keep-target
  --dry-run
  -h, --help
USAGE
}

log() {
  printf '[send-rate-sweep] %s\n' "$*"
}

die() {
  printf '[send-rate-sweep] ERROR: %s\n' "$*" >&2
  exit 1
}

require_uint() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a non-negative integer: $value"
}

require_positive_uint() {
  local name="$1"
  local value="$2"
  require_uint "$name" "$value"
  (( value > 0 )) || die "$name must be greater than zero: $value"
}

require_jq() {
  command -v jq >/dev/null 2>&1 || die "jq is required to extract sweep results; install jq or run with --dry-run"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode) MODE="$2"; shift 2 ;;
    --rates) RATES="$2"; shift 2 ;;
    --duration) DURATION="$2"; shift 2 ;;
    --users) USERS="$2"; shift 2 ;;
    --groups) GROUPS="$2"; shift 2 ;;
    --members) MEMBERS="$2"; shift 2 ;;
    --person-pairs) PERSON_PAIRS="$2"; shift 2 ;;
    --mixed-ratio) MIXED_RATIO="$2"; shift 2 ;;
    --payload-size) PAYLOAD_SIZE="$2"; shift 2 ;;
    --ack-timeout) ACK_TIMEOUT="$2"; shift 2 ;;
    --expected-latency-ms) EXPECTED_LATENCY_MS="$2"; shift 2 ;;
    --inflight-multiplier) INFLIGHT_MULTIPLIER="$2"; shift 2 ;;
    --max-in-flight-cap) MAX_IN_FLIGHT_CAP="$2"; shift 2 ;;
    --out-dir) OUT_DIR="$2"; shift 2 ;;
    --start-target) START_TARGET=1; shift ;;
    --no-start-target) START_TARGET=0; shift ;;
    --clean-target) CLEAN_TARGET=1; START_TARGET=1; shift ;;
    --keep-target) KEEP_TARGET=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

case "$MODE" in
  person|group|mixed) ;;
  *) die "--mode must be person, group, or mixed" ;;
esac
[[ "$RATES" != "" ]] || die "--rates must not be empty"
require_positive_uint "--users" "$USERS"
require_positive_uint "--groups" "$GROUPS"
require_positive_uint "--members" "$MEMBERS"
require_positive_uint "--person-pairs" "$PERSON_PAIRS"
require_uint "--payload-size" "$PAYLOAD_SIZE"
require_positive_uint "--expected-latency-ms" "$EXPECTED_LATENCY_MS"
require_positive_uint "--inflight-multiplier" "$INFLIGHT_MULTIPLIER"
require_positive_uint "--max-in-flight-cap" "$MAX_IN_FLIGHT_CAP"

if [[ "$DRY_RUN" -eq 0 ]]; then
  require_jq
fi

mkdir -p "$OUT_DIR/steps"
printf 'mode,total_qps,status,report_dir\n' > "$OUT_DIR/summary.csv"
{
  printf '# send rate sweep\n\n'
  printf -- '- mode: `%s`\n' "$MODE"
  printf -- '- rates: `%s`\n' "$RATES"
  printf -- '- status: `not-run`\n'
} > "$OUT_DIR/summary.md"

if [[ "$DRY_RUN" -eq 1 ]]; then
  log "dry-run wrote $OUT_DIR"
  exit 0
fi

log "sweep skeleton initialized at $OUT_DIR"
```

Make it executable:

```bash
chmod +x scripts/bench-wukongim-three-node-send-rate-sweep.sh
```

- [ ] **Step 4: Run tests to verify the skeleton passes**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestSendRateSweepScript' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add scripts/bench-wukongim-three-node-send-rate-sweep.sh scripts/smoke_test.go
git commit -m "feat: add send rate sweep script skeleton"
```

---

### Task 2: Render Step Scenarios In Dry Run

**Files:**
- Modify: `scripts/bench-wukongim-three-node-send-rate-sweep.sh`
- Modify: `scripts/smoke_test.go`

- [ ] **Step 1: Write failing dry-run tests for person, group, and mixed scenario rendering**

Add this helper to `scripts/smoke_test.go`:

```go
func runSweepDryRun(t *testing.T, args ...string) (string, string) {
	t.Helper()
	outDir := t.TempDir()
	allArgs := append([]string{
		"--out-dir", outDir,
		"--dry-run",
		"--no-start-target",
		"--rates", "100",
		"--duration", "1s",
		"--expected-latency-ms", "200",
		"--inflight-multiplier", "2",
	}, args...)
	out, err := runBash(t, sweepScriptPath(t), allArgs...)
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	return outDir, out
}
```

Add this test:

```go
func TestSendRateSweepDryRunRendersModeScenarios(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	cases := []struct {
		name       string
		mode       string
		want       []string
		mustNot    []string
	}{
		{
			name: "person",
			mode: "person",
			want: []string{
				"use: identity.person_pairs",
				"use: traffic.send",
				"rate: 100/s",
				"max_in_flight: 40",
				"person_traffic:",
			},
			mustNot: []string{"group_traffic:", "use: wukongim.prepare_group_channels"},
		},
		{
			name: "group",
			mode: "group",
			want: []string{
				"use: wukongim.prepare_group_channels",
				"use: traffic.send",
				"rate: 100/s",
				"max_in_flight: 40",
				"group_traffic:",
			},
			mustNot: []string{"person_traffic:", "use: identity.person_pairs"},
		},
		{
			name: "mixed",
			mode: "mixed",
			want: []string{
				"person_traffic:",
				"group_traffic:",
				"rate: 80/s",
				"rate: 20/s",
				"max_in_flight: 32",
				"max_in_flight: 8",
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			outDir, _ := runSweepDryRun(t, "--mode", tt.mode)
			data, err := os.ReadFile(filepath.Join(outDir, "steps", "0001-100qps", "scenario.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("%s scenario missing %q:\n%s", tt.mode, want, text)
				}
			}
			for _, bad := range tt.mustNot {
				if strings.Contains(text, bad) {
					t.Fatalf("%s scenario should not contain %q:\n%s", tt.mode, bad, text)
				}
			}
		})
	}
}
```

Add this cap test:

```go
func TestSendRateSweepDryRunCapsMaxInFlight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	outDir, _ := runSweepDryRun(t,
		"--mode", "person",
		"--rates", "100000",
		"--max-in-flight-cap", "1234",
	)
	data, err := os.ReadFile(filepath.Join(outDir, "steps", "0001-100000qps", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "max_in_flight: 1234") {
		t.Fatalf("scenario should cap max_in_flight at 1234:\n%s", data)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestSendRateSweepDryRun' -count=1
```

Expected: FAIL because dry-run does not render step scenarios yet.

- [ ] **Step 3: Implement rate splitting, in-flight calculation, and scenario rendering**

Add these functions to the script before argument parsing:

```bash
ceil_div() {
  local numerator="$1"
  local denominator="$2"
  printf '%d\n' $(((numerator + denominator - 1) / denominator))
}

max_in_flight_for_rate() {
  local rate="$1"
  local raw
  raw="$(ceil_div "$((rate * EXPECTED_LATENCY_MS * INFLIGHT_MULTIPLIER))" 1000)"
  (( raw < 1 )) && raw=1
  (( raw > MAX_IN_FLIGHT_CAP )) && raw="$MAX_IN_FLIGHT_CAP"
  printf '%d\n' "$raw"
}

split_rates() {
  local total="$1"
  local person_weight group_weight weight_sum person_rate group_rate
  person_weight="${MIXED_RATIO%%:*}"
  group_weight="${MIXED_RATIO##*:}"
  require_positive_uint "--mixed-ratio person weight" "$person_weight"
  require_positive_uint "--mixed-ratio group weight" "$group_weight"
  weight_sum=$((person_weight + group_weight))
  person_rate="$((total * person_weight / weight_sum))"
  group_rate="$((total - person_rate))"
  printf '%s %s\n' "$person_rate" "$group_rate"
}

step_dir_for() {
  local index="$1"
  local rate="$2"
  printf '%s/steps/%04d-%sqps' "$OUT_DIR" "$index" "$rate"
}
```

Add `render_common_prefix`, `render_group_units`, `render_person_units`, and `render_scenario` to write complete YAML. Use these exact target addresses:

```bash
render_common_prefix() {
  local run_id="$1"
  local report_dir="$2"
  cat <<YAML
version: wkbench/v2

run:
  id: $run_id
  duration: $DURATION
  report_dir: $report_dir

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

  identities:
    use: identity.pool
    spec:
      total: $USERS
      uid_prefix: sweep-u
      device_prefix: sweep-d
      token_prefix: sweep-token

  tokens:
    use: wukongim.prepare_tokens

YAML
}
```

The implementation should render:

- `groups` only when mode is `group` or `mixed`.
- `pairs` only when mode is `person` or `mixed`.
- `sessions` after `[tokens, groups]` for group/mixed and after `[tokens]` for person.
- `person_traffic` with `targets: pairs.targets`.
- `group_traffic` with `targets: groups.targets`.
- One `report.assert` unit per traffic unit.

In the main flow, replace the skeleton summary-only dry-run with a loop:

```bash
IFS=',' read -r -a RATE_VALUES <<< "$RATES"
step_index=0
for total_rate in "${RATE_VALUES[@]}"; do
  total_rate="${total_rate//[[:space:]]/}"
  require_positive_uint "--rates item" "$total_rate"
  step_index=$((step_index + 1))
  step_dir="$(step_dir_for "$step_index" "$total_rate")"
  mkdir -p "$step_dir"
  render_scenario "$step_index" "$total_rate" "$step_dir" > "$step_dir/scenario.yaml"
  printf '%s,%s,%s,%s\n' "$MODE" "$total_rate" "not-run" "$step_dir" >> "$OUT_DIR/summary.csv"
done
```

- [ ] **Step 4: Run tests to verify dry-run rendering passes**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestSendRateSweepDryRun|TestSendRateSweepScriptSyntax' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add scripts/bench-wukongim-three-node-send-rate-sweep.sh scripts/smoke_test.go
git commit -m "feat: render send rate sweep scenarios"
```

---

### Task 3: Execute Steps And Extract Results

**Files:**
- Modify: `scripts/bench-wukongim-three-node-send-rate-sweep.sh`
- Modify: `scripts/smoke_test.go`

- [ ] **Step 1: Write failing tests for execution hooks and summary extraction expressions**

Add this test to `scripts/smoke_test.go`:

```go
func TestSendRateSweepScriptExtractsReportJSONFields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	data, err := os.ReadFile(sweepScriptPath(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`.units[$unit].outputs.summary.value.sendack_ok`,
		`.units[$unit].outputs.summary.value.sendack_errors`,
		`.units[$unit].metrics.sendack_latency.sum`,
		`.units[$unit].metrics.sendack_latency.min`,
		`.units[$unit].metrics.sendack_latency.max`,
		`avg_ms`,
		`summary.csv`,
		`highest_passing_qps`,
		`first_failing_qps`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sweep script missing result extraction fragment %q", want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./scripts -run TestSendRateSweepScriptExtractsReportJSONFields -count=1
```

Expected: FAIL because result extraction is not implemented.

- [ ] **Step 3: Implement validate/run loop**

Add these functions:

```bash
check_ready() {
  local api
  for api in http://127.0.0.1:5011 http://127.0.0.1:5012 http://127.0.0.1:5013; do
    curl -fsS --max-time 3 "$api/readyz" >/dev/null || return 1
  done
}

start_target_if_needed() {
  if [[ "$START_TARGET" -eq 0 ]]; then
    return
  fi
  local args=()
  if [[ "$CLEAN_TARGET" -eq 1 ]]; then
    args+=(--clean)
  fi
  "$ROOT/scripts/start-wukongimv2-three-nodes.sh" "${args[@]}" &
  TARGET_PID="$!"
  local deadline=$((SECONDS + 90))
  until check_ready; do
    if (( SECONDS > deadline )); then
      die "timed out waiting for local three-node target"
    fi
    sleep 1
  done
}

stop_target_if_needed() {
  if [[ -n "$TARGET_PID" && "$KEEP_TARGET" -eq 0 ]]; then
    kill "$TARGET_PID" 2>/dev/null || true
    wait "$TARGET_PID" 2>/dev/null || true
  fi
}

run_step() {
  local step_dir="$1"
  local scenario="$step_dir/scenario.yaml"
  local console="$step_dir/console.txt"
  (
    cd "$ROOT"
    GOWORK=off go run ./cmd/wkbench validate -scenario "$scenario"
    GOWORK=off go run ./cmd/wkbench run -scenario "$scenario"
  ) > "$console" 2>&1
}
```

Add:

```bash
trap stop_target_if_needed EXIT
```

Call `start_target_if_needed` before the step loop when not dry-run. For every real step:

- call `check_ready`
- call `run_step`
- mark failed on non-zero exit
- stop loop after the first failed step

- [ ] **Step 4: Implement report extraction and aggregate summaries**

Add a CSV header that can represent one or two workload rows per step:

```bash
printf 'mode,total_qps,workload,offered_qps,status,sendack_ok,sendack_errors,error_rate,latency_avg_ms,latency_min_ms,latency_max_ms,report_dir\n' > "$OUT_DIR/summary.csv"
```

Add these functions:

```bash
extract_unit_row() {
  local report="$1"
  local unit="$2"
  local mode="$3"
  local total_qps="$4"
  local offered_qps="$5"
  local status="$6"
  jq -r \
    --arg unit "$unit" \
    --arg mode "$mode" \
    --arg total_qps "$total_qps" \
    --arg offered_qps "$offered_qps" \
    --arg status "$status" \
    --arg report_dir "$(dirname "$report")" \
    '
    .units[$unit] as $u
    | ($u.outputs.summary.value.sendack_ok // 0) as $ok
    | ($u.outputs.summary.value.sendack_errors // 0) as $errors
    | (($ok + $errors) | if . == 0 then 0 else ($errors / .) end) as $error_rate
    | ($u.metrics.sendack_latency.count // 0) as $lat_count
    | ($u.metrics.sendack_latency.sum // 0) as $lat_sum
    | ($u.metrics.sendack_latency.min // 0) as $lat_min
    | ($u.metrics.sendack_latency.max // 0) as $lat_max
    | (if $lat_count == 0 then 0 else ($lat_sum / $lat_count * 1000) end) as $avg_ms
    | [$mode, $total_qps, $unit, $offered_qps, $status, $ok, $errors, $error_rate, $avg_ms, ($lat_min * 1000), ($lat_max * 1000), $report_dir]
    | @csv
    ' "$report"
}
```

Generate `summary.md` from the CSV with a simple table. Include:

```text
highest_passing_qps: `$highest_passing_qps`
first_failing_qps: `$first_failing_qps`
```

Do not aggregate mixed latency. Write one row for `person_traffic` and one row for `group_traffic`.

- [ ] **Step 5: Run tests for extraction fragments and script syntax**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestSendRateSweepScript' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add scripts/bench-wukongim-three-node-send-rate-sweep.sh scripts/smoke_test.go
git commit -m "feat: execute send rate sweep steps"
```

---

### Task 4: README And Fast Verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Write failing README test**

Add this test to `scripts/smoke_test.go`:

```go
func TestReadmeDocumentsSendRateSweep(t *testing.T) {
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"bench-wukongim-three-node-send-rate-sweep.sh",
		"--mode mixed",
		"--rates 100,200,500",
		"--duration 2m",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("README missing sweep usage %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off go test ./scripts -run TestReadmeDocumentsSendRateSweep -count=1
```

Expected: FAIL because README does not document the sweep script yet.

- [ ] **Step 3: Add README usage**

Add this section near the existing three-node send-rate commands:

```markdown
Sweep a three-node target to find the highest passing send-link QPS:

```bash
./scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 100,200,500 \
  --duration 2m \
  --no-start-target
```

For an end-to-end local run that starts and stops the three-node target:

```bash
./scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 10,20 \
  --duration 5s \
  --start-target \
  --clean-target
```
```

- [ ] **Step 4: Run README test**

Run:

```bash
GOWORK=off go test ./scripts -run TestReadmeDocumentsSendRateSweep -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add README.md scripts/smoke_test.go
git commit -m "docs: document send rate sweep"
```

---

### Task 5: Final Verification And Short Real Sweep

**Files:**
- No planned source edits.

- [ ] **Step 1: Run full test suite**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS for all packages.

- [ ] **Step 2: Run dry-run sweep**

Run:

```bash
./scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 10,20 \
  --duration 1s \
  --dry-run \
  --out-dir /tmp/wkbench-send-rate-sweep-dry
```

Expected:

- exits `0`
- writes `/tmp/wkbench-send-rate-sweep-dry/steps/0001-10qps/group/scenario.yaml`
- writes `/tmp/wkbench-send-rate-sweep-dry/steps/0001-10qps/person/scenario.yaml`
- writes `/tmp/wkbench-send-rate-sweep-dry/steps/0002-20qps/group/scenario.yaml`
- writes `/tmp/wkbench-send-rate-sweep-dry/steps/0002-20qps/person/scenario.yaml`
- writes `/tmp/wkbench-send-rate-sweep-dry/summary.csv`

- [ ] **Step 3: Run one short real local three-node sweep**

Run:

```bash
./scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 10,20 \
  --duration 5s \
  --start-target \
  --clean-target \
  --out-dir ./reports/send-rate-sweep/smoke
```

Expected:

- exits `0`
- starts all three local WuKongIM nodes
- runs both rate steps unless a step fails
- stops the local target at exit
- writes `reports/send-rate-sweep/smoke/summary.md`
- writes `reports/send-rate-sweep/smoke/summary.csv`

- [ ] **Step 4: Inspect summary**

Run:

```bash
sed -n '1,160p' reports/send-rate-sweep/smoke/summary.md
```

Expected:

- contains `highest_passing_qps`
- contains `first_failing_qps`
- contains rows for `person_traffic` and `group_traffic`
- latency values are in `ms`

- [ ] **Step 5: Ensure no target processes remain**

Run:

```bash
lsof -nP -iTCP:5011 -iTCP:5012 -iTCP:5013 -iTCP:5111 -iTCP:5112 -iTCP:5113 -sTCP:LISTEN || true
```

Expected: no output when `--keep-target` was not used.

- [ ] **Step 6: Check diff and status**

Run:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only intended source/docs changes, or clean if all implementation commits are complete.
