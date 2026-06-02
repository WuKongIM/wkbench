#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCENARIO="${WKBENCH_SCENARIO:-"$ROOT/examples/wukongim-send-rate-mixed.yaml"}"

cd "$ROOT"

echo "wkbench mixed send-rate smoke: validating $SCENARIO"
GOWORK=off go run ./cmd/wkbench validate -scenario "$SCENARIO"

echo "wkbench mixed send-rate smoke: running $SCENARIO"
GOWORK=off go run ./cmd/wkbench run -scenario "$SCENARIO"
