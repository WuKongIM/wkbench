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

if [[ "$DRY_RUN" -eq 1 ]]; then
  log "dry-run wrote $OUT_DIR"
  exit 0
fi

log "sweep skeleton initialized at $OUT_DIR"
