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
      count: $GROUPS
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
      GROUPS="$2"
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

if [[ "$DRY_RUN" -eq 1 ]]; then
  log "dry-run wrote $OUT_DIR"
  exit 0
fi

log "sweep skeleton initialized at $OUT_DIR"
