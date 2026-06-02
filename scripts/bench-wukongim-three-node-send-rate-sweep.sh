#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="mixed"
RATES="10,20"
DURATION="10s"
USERS=100
GROUP_COUNT=10
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

check_ready() {
  local api
  for api in http://127.0.0.1:5011 http://127.0.0.1:5012 http://127.0.0.1:5013; do
    curl -fsS --max-time 3 "$api/readyz" >/dev/null 2>&1 || return 1
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
  "$ROOT/scripts/start-wukongimv2-three-nodes.sh" "${args[@]}" > "$OUT_DIR/target-console.txt" 2>&1 &
  TARGET_PID="$!"
  local deadline=$((SECONDS + 90))
  until check_ready; do
    if ! kill -0 "$TARGET_PID" 2>/dev/null; then
      cat "$OUT_DIR/target-console.txt" >&2 2>/dev/null || true
      die "local three-node target exited before readiness"
    fi
    if (( SECONDS > deadline )); then
      cat "$OUT_DIR/target-console.txt" >&2 2>/dev/null || true
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

render_group_prep() {
  cat <<YAML
  groups:
    use: wukongim.prepare_group_channels
    spec:
      profile: sweep
      count: $GROUP_COUNT
      members_per_channel: $MEMBERS
      overlap: disallowed
      batch_size: 1000

YAML
}

render_person_prep() {
  cat <<YAML
  pairs:
    use: identity.person_pairs
    spec:
      count: $PERSON_PAIRS
      mode: ring
      bidirectional: true

YAML
}

render_sessions() {
  case "$MODE" in
    person)
      cat <<YAML
  sessions:
    use: wkproto.session_pool
    after: [tokens]
    spec:
      connect_rate: 100/s

YAML
      ;;
    group|mixed)
      cat <<YAML
  sessions:
    use: wkproto.session_pool
    after: [tokens, groups]
    spec:
      connect_rate: 100/s

YAML
      ;;
  esac
}

render_person_traffic() {
  local rate="$1"
  local max_in_flight
  max_in_flight="$(max_in_flight_for_rate "$rate")"
  cat <<YAML
  person_traffic:
    use: traffic.send
    inputs:
      targets: pairs.targets
      sender: sessions.message_sender
    spec:
      rate: $rate/s
      payload_size: $PAYLOAD_SIZE
      sender_pick: round_robin
      max_in_flight: $max_in_flight
      ack_timeout: $ACK_TIMEOUT

  person_limits:
    use: report.assert
    inputs:
      summary: person_traffic.summary
    spec:
      rules:
        - metric: sendack_error_rate
          op: eq
          value: 0

YAML
}

render_group_traffic() {
  local rate="$1"
  local max_in_flight
  max_in_flight="$(max_in_flight_for_rate "$rate")"
  cat <<YAML
  group_traffic:
    use: traffic.send
    inputs:
      targets: groups.targets
      sender: sessions.message_sender
    spec:
      rate: $rate/s
      payload_size: $PAYLOAD_SIZE
      sender_pick: round_robin
      max_in_flight: $max_in_flight
      ack_timeout: $ACK_TIMEOUT

  group_limits:
    use: report.assert
    inputs:
      summary: group_traffic.summary
    spec:
      rules:
        - metric: sendack_error_rate
          op: eq
          value: 0

YAML
}

render_scenario() {
  local index="$1"
  local total_rate="$2"
  local step_dir="$3"
  local run_id="send-rate-sweep-${MODE}-${index}-${total_rate}qps"
  local person_rate group_rate

  render_common_prefix "$run_id" "$step_dir"
  case "$MODE" in
    person)
      render_person_prep
      render_sessions
      render_person_traffic "$total_rate"
      ;;
    group)
      render_group_prep
      render_sessions
      render_group_traffic "$total_rate"
      ;;
    mixed)
      read -r person_rate group_rate < <(split_rates "$total_rate")
      render_group_prep
      render_person_prep
      render_sessions
      render_group_traffic "$group_rate"
      render_person_traffic "$person_rate"
      ;;
  esac
}

offered_rates_for_mode() {
  local total_rate="$1"
  local person_rate group_rate
  case "$MODE" in
    person)
      printf 'person_traffic:%s\n' "$total_rate"
      ;;
    group)
      printf 'group_traffic:%s\n' "$total_rate"
      ;;
    mixed)
      read -r person_rate group_rate < <(split_rates "$total_rate")
      printf 'group_traffic:%s\n' "$group_rate"
      printf 'person_traffic:%s\n' "$person_rate"
      ;;
  esac
}

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
    (.units[$unit].outputs.summary.value.sendack_ok // 0) as $ok
    | (.units[$unit].outputs.summary.value.sendack_errors // 0) as $errors
    | (($ok + $errors) | if . == 0 then 0 else ($errors / .) end) as $error_rate
    | (.units[$unit].metrics.sendack_latency.count // 0) as $lat_count
    | (.units[$unit].metrics.sendack_latency.sum // 0) as $lat_sum
    | (.units[$unit].metrics.sendack_latency.min // 0) as $lat_min
    | (.units[$unit].metrics.sendack_latency.max // 0) as $lat_max
    | (if $lat_count == 0 then 0 else ($lat_sum / $lat_count * 1000) end) as $avg_ms
    | [$mode, $total_qps, $unit, $offered_qps, $status, $ok, $errors, $error_rate, $avg_ms, ($lat_min * 1000), ($lat_max * 1000), $report_dir]
    | @csv
    ' "$report"
}

extract_unit_values() {
  local report="$1"
  local unit="$2"
  jq -r \
    --arg unit "$unit" \
    '
    (.units[$unit].outputs.summary.value.sendack_ok // 0) as $ok
    | (.units[$unit].outputs.summary.value.sendack_errors // 0) as $errors
    | (($ok + $errors) | if . == 0 then 0 else ($errors / .) end) as $error_rate
    | (.units[$unit].metrics.sendack_latency.count // 0) as $lat_count
    | (.units[$unit].metrics.sendack_latency.sum // 0) as $lat_sum
    | (.units[$unit].metrics.sendack_latency.min // 0) as $lat_min
    | (.units[$unit].metrics.sendack_latency.max // 0) as $lat_max
    | (if $lat_count == 0 then 0 else ($lat_sum / $lat_count * 1000) end) as $avg_ms
    | [$ok, $errors, $error_rate, $avg_ms, ($lat_min * 1000), ($lat_max * 1000)]
    | @tsv
    ' "$report"
}

append_missing_unit_result() {
  local unit="$1"
  local total_qps="$2"
  local offered_qps="$3"
  local status="$4"
  local step_dir="$5"
  printf '%s,%s,%s,%s,%s,0,0,0,0,0,0,%s\n' "$MODE" "$total_qps" "$unit" "$offered_qps" "$status" "$step_dir" >> "$OUT_DIR/summary.csv"
  printf '| `%s` | `%s` | `%s` | `%s` | `%s` | `0` | `0` | `0.000000` | `0.00ms` | `0.00ms` | `0.00ms` | `%s` |\n' "$MODE" "$total_qps" "$unit" "$offered_qps" "$status" "$step_dir" >> "$SUMMARY_ROWS"
}

append_unit_result() {
  local report="$1"
  local unit="$2"
  local total_qps="$3"
  local offered_qps="$4"
  local status="$5"
  local step_dir="$6"
  local values ok errors error_rate avg_ms min_ms max_ms

  if [[ ! -f "$report" ]]; then
    append_missing_unit_result "$unit" "$total_qps" "$offered_qps" "$status" "$step_dir"
    return
  fi

  extract_unit_row "$report" "$unit" "$MODE" "$total_qps" "$offered_qps" "$status" >> "$OUT_DIR/summary.csv"
  values="$(extract_unit_values "$report" "$unit" || true)"
  if [[ -z "$values" ]]; then
    values=$'0\t0\t0\t0\t0\t0'
  fi
  IFS=$'\t' read -r ok errors error_rate avg_ms min_ms max_ms <<< "$values"
  printf '| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | `%.6f` | `%.2fms` | `%.2fms` | `%.2fms` | `%s` |\n' \
    "$MODE" "$total_qps" "$unit" "$offered_qps" "$status" "$ok" "$errors" "$error_rate" "$avg_ms" "$min_ms" "$max_ms" "$step_dir" >> "$SUMMARY_ROWS"
}

append_step_results() {
  local step_dir="$1"
  local total_qps="$2"
  local status="$3"
  local report="$step_dir/report.json"
  local pair unit offered_qps
  while IFS= read -r pair; do
    [[ -n "$pair" ]] || continue
    unit="${pair%%:*}"
    offered_qps="${pair##*:}"
    append_unit_result "$report" "$unit" "$total_qps" "$offered_qps" "$status" "$step_dir"
  done < <(offered_rates_for_mode "$total_qps")
}

append_not_run_results() {
  local step_dir="$1"
  local total_qps="$2"
  local pair unit offered_qps
  while IFS= read -r pair; do
    [[ -n "$pair" ]] || continue
    unit="${pair%%:*}"
    offered_qps="${pair##*:}"
    append_missing_unit_result "$unit" "$total_qps" "$offered_qps" "not-run" "$step_dir"
  done < <(offered_rates_for_mode "$total_qps")
}

write_summary_markdown() {
  local status="$1"
  local highest_passing_qps="$2"
  local first_failing_qps="$3"
  {
    printf '# send rate sweep\n\n'
    printf -- '- mode: `%s`\n' "$MODE"
    printf -- '- rates: `%s`\n' "$RATES"
    printf -- '- status: `%s`\n' "$status"
    printf -- '- highest_passing_qps: `%s`\n' "$highest_passing_qps"
    printf -- '- first_failing_qps: `%s`\n\n' "$first_failing_qps"
    printf '| mode | total_qps | workload | offered_qps | status | sendack_ok | sendack_errors | error_rate | latency_avg | latency_min | latency_max | report_dir |\n'
    printf '| --- | ---: | --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n'
    cat "$SUMMARY_ROWS"
  } > "$OUT_DIR/summary.md"
}

trap stop_target_if_needed EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      [[ $# -ge 2 ]] || die "--mode requires a value"
      MODE="$2"
      shift 2
      ;;
    --rates)
      [[ $# -ge 2 ]] || die "--rates requires a value"
      RATES="$2"
      shift 2
      ;;
    --duration)
      [[ $# -ge 2 ]] || die "--duration requires a value"
      DURATION="$2"
      shift 2
      ;;
    --users)
      [[ $# -ge 2 ]] || die "--users requires a value"
      USERS="$2"
      shift 2
      ;;
    --groups)
      [[ $# -ge 2 ]] || die "--groups requires a value"
      GROUP_COUNT="$2"
      shift 2
      ;;
    --members)
      [[ $# -ge 2 ]] || die "--members requires a value"
      MEMBERS="$2"
      shift 2
      ;;
    --person-pairs)
      [[ $# -ge 2 ]] || die "--person-pairs requires a value"
      PERSON_PAIRS="$2"
      shift 2
      ;;
    --mixed-ratio)
      [[ $# -ge 2 ]] || die "--mixed-ratio requires a value"
      MIXED_RATIO="$2"
      shift 2
      ;;
    --payload-size)
      [[ $# -ge 2 ]] || die "--payload-size requires a value"
      PAYLOAD_SIZE="$2"
      shift 2
      ;;
    --ack-timeout)
      [[ $# -ge 2 ]] || die "--ack-timeout requires a value"
      ACK_TIMEOUT="$2"
      shift 2
      ;;
    --expected-latency-ms)
      [[ $# -ge 2 ]] || die "--expected-latency-ms requires a value"
      EXPECTED_LATENCY_MS="$2"
      shift 2
      ;;
    --inflight-multiplier)
      [[ $# -ge 2 ]] || die "--inflight-multiplier requires a value"
      INFLIGHT_MULTIPLIER="$2"
      shift 2
      ;;
    --max-in-flight-cap)
      [[ $# -ge 2 ]] || die "--max-in-flight-cap requires a value"
      MAX_IN_FLIGHT_CAP="$2"
      shift 2
      ;;
    --out-dir)
      [[ $# -ge 2 ]] || die "--out-dir requires a value"
      OUT_DIR="$2"
      shift 2
      ;;
    --start-target)
      START_TARGET=1
      shift
      ;;
    --no-start-target)
      START_TARGET=0
      shift
      ;;
    --clean-target)
      CLEAN_TARGET=1
      START_TARGET=1
      shift
      ;;
    --keep-target)
      KEEP_TARGET=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

case "$MODE" in
  person|group|mixed) ;;
  *) die "--mode must be person, group, or mixed" ;;
esac
[[ "$RATES" != "" ]] || die "--rates must not be empty"
require_positive_uint "--users" "$USERS"
require_positive_uint "--groups" "$GROUP_COUNT"
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
SUMMARY_ROWS="$OUT_DIR/.summary-rows.md"
: > "$SUMMARY_ROWS"
printf 'mode,total_qps,workload,offered_qps,status,sendack_ok,sendack_errors,error_rate,latency_avg_ms,latency_min_ms,latency_max_ms,report_dir\n' > "$OUT_DIR/summary.csv"

IFS=',' read -r -a RATE_VALUES <<< "$RATES"
step_index=0
highest_passing_qps="none"
first_failing_qps="none"
sweep_status="not-run"

if [[ "$DRY_RUN" -eq 0 ]]; then
  start_target_if_needed
fi

for total_rate in "${RATE_VALUES[@]}"; do
  total_rate="${total_rate//[[:space:]]/}"
  require_positive_uint "--rates item" "$total_rate"
  step_index=$((step_index + 1))
  step_dir="$(step_dir_for "$step_index" "$total_rate")"
  mkdir -p "$step_dir"
  render_scenario "$step_index" "$total_rate" "$step_dir" > "$step_dir/scenario.yaml"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    append_not_run_results "$step_dir" "$total_rate"
    continue
  fi

  step_status="passed"
  if ! check_ready; then
    step_status="failed"
    printf 'target readiness check failed before step %s\n' "$total_rate" > "$step_dir/console.txt"
  elif ! run_step "$step_dir"; then
    step_status="failed"
  fi

  append_step_results "$step_dir" "$total_rate" "$step_status"
  if [[ "$step_status" == "passed" ]]; then
    highest_passing_qps="$total_rate"
    sweep_status="completed"
  else
    first_failing_qps="$total_rate"
    sweep_status="failed"
    break
  fi
done

if [[ "$DRY_RUN" -eq 1 ]]; then
  write_summary_markdown "not-run" "$highest_passing_qps" "$first_failing_qps"
  rm -f "$SUMMARY_ROWS"
  log "dry-run wrote $OUT_DIR"
  exit 0
fi

write_summary_markdown "$sweep_status" "$highest_passing_qps" "$first_failing_qps"
rm -f "$SUMMARY_ROWS"
log "sweep completed at $OUT_DIR"
