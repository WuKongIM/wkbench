#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCENARIO="${WKBENCH_SCENARIO:-"$ROOT/examples/wukongim-group-send.yaml"}"

cd "$ROOT"

echo "wkbench smoke: validating $SCENARIO"
GOWORK=off go run ./cmd/wkbench validate -scenario "$SCENARIO"

echo "wkbench smoke: running $SCENARIO"
GOWORK=off go run ./cmd/wkbench run -scenario "$SCENARIO"
