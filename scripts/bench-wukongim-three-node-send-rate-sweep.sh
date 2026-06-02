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
COLLECT_METRICS=0
METRICS_INTERVAL="1s"
METRICS_INCLUDE="wk_.*,wukongim_.*"
METRICS_EXCLUDE="go_.*,process_.*"

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
  --collect-metrics
  --metrics-interval D
  --metrics-include REGEX_LIST
  --metrics-exclude REGEX_LIST
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

trim_space() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s\n' "$value"
}

yaml_double_quote() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}

render_csv_list_field() {
  local name="$1"
  local csv="$2"
  local raw item
  local -a entries=()
  local -a items=()
  IFS=',' read -r -a entries <<< "$csv"
  for raw in "${entries[@]}"; do
    item="$(trim_space "$raw")"
    [[ -n "$item" ]] || continue
    items+=("$item")
  done
  if [[ "${#items[@]}" -eq 0 ]]; then
    printf '      %s: []\n' "$name"
    return
  fi
  printf '      %s:\n' "$name"
  for item in "${items[@]}"; do
    printf '        - '
    yaml_double_quote "$item"
    printf '\n'
  done
}

split_rates() {
  local total="$1"
  local person_weight group_weight weight_sum person_rate group_rate
  person_weight="${MIXED_RATIO%%:*}"
  group_weight="${MIXED_RATIO##*:}"
  require_positive_uint "--mixed-ratio person weight" "$person_weight"
  require_positive_uint "--mixed-ratio group weight" "$group_weight"
  if (( total == 1 )); then
    if (( person_weight >= group_weight )); then
      printf '1 0\n'
    else
      printf '0 1\n'
    fi
    return
  fi
  weight_sum=$((person_weight + group_weight))
  person_rate="$((total * person_weight / weight_sum))"
  group_rate="$((total - person_rate))"
  if (( person_rate == 0 )); then
    person_rate=1
    group_rate=$((total - 1))
  fi
  if (( group_rate == 0 )); then
    group_rate=1
    person_rate=$((total - 1))
  fi
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
  local identity_prefix="$3"
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

YAML
  if [[ "$COLLECT_METRICS" -eq 1 ]]; then
    cat <<YAML
  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: $METRICS_INTERVAL
      timeout: 5s
      path: /metrics
YAML
    render_csv_list_field include "$METRICS_INCLUDE"
    render_csv_list_field exclude "$METRICS_EXCLUDE"
    cat <<YAML
      fail_on_scrape_error: false

YAML
  fi
  cat <<YAML
  identities:
    use: identity.pool
YAML
  if [[ "$COLLECT_METRICS" -eq 1 ]]; then
    cat <<YAML
    after: [metrics]
YAML
  fi
  cat <<YAML
    spec:
      total: $USERS
      uid_prefix: sweep-$identity_prefix-u
      device_prefix: sweep-$identity_prefix-d
      token_prefix: sweep-$identity_prefix-token

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
  local scenario_mode="$1"
  case "$scenario_mode" in
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
YAML
  if [[ "$COLLECT_METRICS" -eq 1 ]]; then
    cat <<YAML
    after: [metrics]
YAML
  fi
  cat <<YAML
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
YAML
  if [[ "$COLLECT_METRICS" -eq 1 ]]; then
    cat <<YAML
    after: [metrics]
YAML
  fi
  cat <<YAML
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
  local scenario_mode="$1"
  local index="$2"
  local rate="$3"
  local report_dir="$4"
  local run_id="send-rate-sweep-${scenario_mode}-${index}-${rate}qps"

  render_common_prefix "$run_id" "$report_dir" "$scenario_mode"
  case "$scenario_mode" in
    person)
      render_person_prep
      render_sessions "$scenario_mode"
      render_person_traffic "$rate"
      ;;
    group)
      render_group_prep
      render_sessions "$scenario_mode"
      render_group_traffic "$rate"
      ;;
  esac
}

render_step_scenarios() {
  local index="$1"
  local total_rate="$2"
  local step_dir="$3"
  local person_rate group_rate
  local group_dir person_dir
  case "$MODE" in
    person|group)
      render_scenario "$MODE" "$index" "$total_rate" "$step_dir" > "$step_dir/scenario.yaml"
      ;;
    mixed)
      read -r person_rate group_rate < <(split_rates "$total_rate")
      if (( group_rate > 0 )); then
        mkdir -p "$step_dir/group"
        render_scenario "group" "$index" "$group_rate" "$step_dir/group" > "$step_dir/group/scenario.yaml"
      fi
      if (( person_rate > 0 )); then
        mkdir -p "$step_dir/person"
        render_scenario "person" "$index" "$person_rate" "$step_dir/person" > "$step_dir/person/scenario.yaml"
      fi
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
      if (( group_rate > 0 )); then
        printf 'group_traffic:%s\n' "$group_rate"
      fi
      if (( person_rate > 0 )); then
        printf 'person_traffic:%s\n' "$person_rate"
      fi
      ;;
  esac
}

workload_dir_for_unit() {
  local step_dir="$1"
  local unit="$2"
  if [[ "$MODE" != "mixed" ]]; then
    printf '%s\n' "$step_dir"
    return
  fi
  case "$unit" in
    group_traffic)
      printf '%s/group\n' "$step_dir"
      ;;
    person_traffic)
      printf '%s/person\n' "$step_dir"
      ;;
    *)
      printf '%s\n' "$step_dir"
      ;;
  esac
}

run_mixed_step() {
  local step_dir="$1"
  local total_rate="$2"
  local person_rate group_rate
  local group_dir person_dir
  local group_pid="" person_pid=""
  local group_status=0 person_status=0

  read -r person_rate group_rate < <(split_rates "$total_rate")
  if (( group_rate > 0 )); then
    group_dir="$step_dir/group"
    run_step "$group_dir" &
    group_pid="$!"
  fi
  if (( person_rate > 0 )); then
    person_dir="$step_dir/person"
    run_step "$person_dir" &
    person_pid="$!"
  fi
  if [[ -n "$group_pid" ]]; then
    wait "$group_pid" || group_status=$?
  fi
  if [[ -n "$person_pid" ]]; then
    wait "$person_pid" || person_status=$?
  fi
  [[ "$group_status" -eq 0 && "$person_status" -eq 0 ]]
}

run_step_workloads() {
  local step_dir="$1"
  local total_rate="$2"
  case "$MODE" in
    mixed)
      run_mixed_step "$step_dir" "$total_rate"
      ;;
    person|group)
      run_step "$step_dir"
      ;;
  esac
}

extract_unit_row() {
  local report="$1"
  local unit="$2"
  local mode="$3"
  local total_qps="$4"
  local target_qps="$5"
  local status="$6"
  jq -r \
    --arg unit "$unit" \
    --arg mode "$mode" \
    --arg total_target_qps "$total_qps" \
    --arg target_qps "$target_qps" \
    --arg status "$status" \
    --arg report_dir "$(dirname "$report")" \
    '
    (.units[$unit].outputs.summary.value.sendack_ok // 0) as $ok
    | (.units[$unit].outputs.summary.value.sendack_errors // 0) as $errors
    | (.units[$unit].outputs.summary.value.elapsed_ms // 0) as $elapsed_ms
    | (.units[$unit].metrics.send_attempt_total.sum // .units[$unit].metrics.send_attempt_total.count // ($ok + $errors)) as $planned
    | (($ok + $errors) | if . == 0 then 0 else ($errors / .) end) as $error_rate
    | (if $elapsed_ms == 0 then 0 else ($ok / ($elapsed_ms / 1000)) end) as $actual_qps
    | (.units[$unit].metrics.sendack_latency.p95 // 0) as $lat_p95
    | (.units[$unit].metrics.sendack_latency.p99 // 0) as $lat_p99
    | [$mode, $total_target_qps, $unit, $target_qps, $actual_qps, $status, $planned, $ok, $errors, $error_rate, ($lat_p95 * 1000), ($lat_p99 * 1000), $report_dir]
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
    | (.units[$unit].outputs.summary.value.elapsed_ms // 0) as $elapsed_ms
    | (.units[$unit].metrics.send_attempt_total.sum // .units[$unit].metrics.send_attempt_total.count // ($ok + $errors)) as $planned
    | (($ok + $errors) | if . == 0 then 0 else ($errors / .) end) as $error_rate
    | (if $elapsed_ms == 0 then 0 else ($ok / ($elapsed_ms / 1000)) end) as $actual_qps
    | (.units[$unit].metrics.sendack_latency.p95 // 0) as $lat_p95
    | (.units[$unit].metrics.sendack_latency.p99 // 0) as $lat_p99
    | [$planned, $ok, $errors, $elapsed_ms, $error_rate, $actual_qps, ($lat_p95 * 1000), ($lat_p99 * 1000)]
    | @tsv
    ' "$report"
}

append_missing_unit_result() {
  local unit="$1"
  local total_qps="$2"
  local target_qps="$3"
  local status="$4"
  local step_dir="$5"
  printf '%s,%s,%s,%s,0.00,%s,0,0,0,0,0,0,%s\n' "$MODE" "$total_qps" "$unit" "$target_qps" "$status" "$step_dir" >> "$OUT_DIR/summary.csv"
  printf '| `%s` | `%s` | `%s` | `%s` | `0.00` | `%s` | `0` | `0` | `0` | `0.000000` | `0.00ms` | `0.00ms` | `%s` |\n' "$MODE" "$total_qps" "$unit" "$target_qps" "$status" "$step_dir" >> "$SUMMARY_ROWS"
}

error_rate_for_counts() {
  local ok="$1"
  local errors="$2"
  local total=$((ok + errors))
  local scaled
  if (( total == 0 )); then
    printf '0.000000\n'
    return
  fi
  scaled=$(((errors * 1000000 + total / 2) / total))
  printf '%d.%06d\n' "$((scaled / 1000000))" "$((scaled % 1000000))"
}

actual_qps_for_counts() {
  local ok="$1"
  local elapsed_ms="$2"
  local scaled
  if (( elapsed_ms <= 0 )); then
    printf '0.00\n'
    return
  fi
  scaled=$(((ok * 100000 + elapsed_ms / 2) / elapsed_ms))
  printf '%d.%02d\n' "$((scaled / 100))" "$((scaled % 100))"
}

append_total_result() {
  local total_qps="$1"
  local status="$2"
  local planned="$3"
  local completed="$4"
  local errors="$5"
  local actual_qps="$6"
  local step_dir="$7"
  local error_rate
  error_rate="$(error_rate_for_counts "$completed" "$errors")"
  printf '%s,%s,total,%s,%s,%s,%s,%s,%s,%s,,,%s\n' "$MODE" "$total_qps" "$total_qps" "$actual_qps" "$status" "$planned" "$completed" "$errors" "$error_rate" "$step_dir" >> "$OUT_DIR/summary.csv"
  printf '| `%s` | `%s` | `total` | `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | `%.6f` | `n/a` | `n/a` | `%s` |\n' \
    "$MODE" "$total_qps" "$total_qps" "$actual_qps" "$status" "$planned" "$completed" "$errors" "$error_rate" "$step_dir" >> "$SUMMARY_ROWS"
}

unit_counts_elapsed() {
  local report="$1"
  local unit="$2"
  if [[ ! -f "$report" ]]; then
    printf '0 0 0 0\n'
    return
  fi
  jq -r \
    --arg unit "$unit" \
    '[
      (.units[$unit].outputs.summary.value.sendack_ok // 0),
      (.units[$unit].outputs.summary.value.sendack_errors // 0),
      (.units[$unit].outputs.summary.value.elapsed_ms // 0),
      (.units[$unit].metrics.send_attempt_total.sum // .units[$unit].metrics.send_attempt_total.count // ((.units[$unit].outputs.summary.value.sendack_ok // 0) + (.units[$unit].outputs.summary.value.sendack_errors // 0)))
    ] | @tsv' "$report"
}

append_unit_result() {
  local report="$1"
  local unit="$2"
  local total_qps="$3"
  local target_qps="$4"
  local status="$5"
  local step_dir="$6"
  local values planned completed errors elapsed_ms error_rate actual_qps p95_ms p99_ms

  if [[ ! -f "$report" ]]; then
    append_missing_unit_result "$unit" "$total_qps" "$target_qps" "$status" "$step_dir"
    return
  fi

  extract_unit_row "$report" "$unit" "$MODE" "$total_qps" "$target_qps" "$status" >> "$OUT_DIR/summary.csv"
  values="$(extract_unit_values "$report" "$unit" || true)"
  if [[ -z "$values" ]]; then
    values=$'0\t0\t0\t0\t0\t0\t0\t0'
  fi
  IFS=$'\t' read -r planned completed errors elapsed_ms error_rate actual_qps p95_ms p99_ms <<< "$values"
  printf '| `%s` | `%s` | `%s` | `%s` | `%.2f` | `%s` | `%s` | `%s` | `%s` | `%.6f` | `%.2fms` | `%.2fms` | `%s` |\n' \
    "$MODE" "$total_qps" "$unit" "$target_qps" "$actual_qps" "$status" "$planned" "$completed" "$errors" "$error_rate" "$p95_ms" "$p99_ms" "$step_dir" >> "$SUMMARY_ROWS"
}

append_step_results() {
  local step_dir="$1"
  local total_qps="$2"
  local status="$3"
  local pair unit target_qps workload_dir report
  local total_planned=0 total_completed=0 total_errors=0 max_elapsed_ms=0 completed errors elapsed_ms planned actual_qps
  while IFS= read -r pair; do
    [[ -n "$pair" ]] || continue
    unit="${pair%%:*}"
    target_qps="${pair##*:}"
    workload_dir="$(workload_dir_for_unit "$step_dir" "$unit")"
    report="$workload_dir/report.json"
    append_unit_result "$report" "$unit" "$total_qps" "$target_qps" "$status" "$workload_dir"
    read -r completed errors elapsed_ms planned < <(unit_counts_elapsed "$report" "$unit")
    total_planned=$((total_planned + planned))
    total_completed=$((total_completed + completed))
    total_errors=$((total_errors + errors))
    if (( elapsed_ms > max_elapsed_ms )); then
      max_elapsed_ms="$elapsed_ms"
    fi
  done < <(offered_rates_for_mode "$total_qps")
  if [[ "$MODE" == "mixed" ]]; then
    actual_qps="$(actual_qps_for_counts "$total_completed" "$max_elapsed_ms")"
    append_total_result "$total_qps" "$status" "$total_planned" "$total_completed" "$total_errors" "$actual_qps" "$step_dir"
  fi
}

append_not_run_results() {
  local step_dir="$1"
  local total_qps="$2"
  local pair unit target_qps workload_dir
  while IFS= read -r pair; do
    [[ -n "$pair" ]] || continue
    unit="${pair%%:*}"
    target_qps="${pair##*:}"
    workload_dir="$(workload_dir_for_unit "$step_dir" "$unit")"
    append_missing_unit_result "$unit" "$total_qps" "$target_qps" "not-run" "$workload_dir"
  done < <(offered_rates_for_mode "$total_qps")
  if [[ "$MODE" == "mixed" ]]; then
    append_total_result "$total_qps" "not-run" 0 0 0 "0.00" "$step_dir"
  fi
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
    printf '| mode | total_target_qps | workload | target_qps | actual_qps | status | planned_messages | completed_messages | sendack_errors | error_rate | latency_p95 | latency_p99 | report_dir |\n'
    printf '| --- | ---: | --- | ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n'
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
printf 'mode,total_target_qps,workload,target_qps,actual_qps,status,planned_messages,completed_messages,sendack_errors,error_rate,latency_p95_ms,latency_p99_ms,report_dir\n' > "$OUT_DIR/summary.csv"

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
  render_step_scenarios "$step_index" "$total_rate" "$step_dir"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    append_not_run_results "$step_dir" "$total_rate"
    continue
  fi

  step_status="passed"
  if ! check_ready; then
    step_status="failed"
    printf 'target readiness check failed before step %s\n' "$total_rate" > "$step_dir/console.txt"
  elif ! run_step_workloads "$step_dir" "$total_rate"; then
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
