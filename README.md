# wkbench

`wkbench` is a composable benchmark toolkit for WuKongIM and related black-box messaging workloads.

This repository starts the v2 architecture: a small kernel runs scenario graphs, while independent units provide capabilities through versioned ports. Units do not import each other. Composition happens only in scenario YAML.

## Quick Start

Run the dry-run group SEND example:

```bash
GOWORK=off go run ./cmd/wkbench run -scenario ./examples/group-send.yaml
```

Explain a scenario graph before running it:

```bash
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/group-send.yaml -format json
```

`explain` validates specs and wiring, then prints the execution order and resolved input bindings. It does not run units, create reports, or touch target services.

Plan deterministic unit work before running it:

```bash
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml -format json
```

`plan` validates the scenario and calls each unit's `Plan` phase, then prints per-unit plan status and shard counts. It does not run units, publish outputs, write reports, or touch target services.

List built-in units:

```bash
GOWORK=off go run ./cmd/wkbench list-units
```

## External Plugins

Phase 1 supports loading external plugin executables with the global `-plugin`
flag:

```bash
GOWORK=off go build -o /tmp/wkbench-demo-plugin ./plugins/demo/cmd/wkbench-demo-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin list-units
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin validate -scenario ./examples/plugin-echo.yaml
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin run -scenario ./examples/plugin-echo.yaml
```

Scenario YAML can reference external units as `<plugin-name>:<kind>`, for
example `wkbench.demo:demo.echo/v1`. During Phase 1, official units still run
in-process while external plugin units are registered as remote proxies. The
final architecture will remove direct unit registration from the host binary
after migration. See [docs/plugin-authoring.md](docs/plugin-authoring.md) for
authoring details and Phase 1 limits.

Create a new unit skeleton:

```bash
GOWORK=off go run ./cmd/wkbench new-unit -kind demo.group_send_probe/v1 -dir ./units/demo/group_send_probe
GOWORK=off go test ./units/demo/group_send_probe
```

Run tests:

```bash
GOWORK=off go test ./...
```

## Current Built-In Units

- `core.static_groups/v1`: produces deterministic in-memory group channels.
- `core.fake_group_sender/v1`: produces a fake WKProto group sender for examples and tests.
- `core.fake_message_sender/v1`: produces a fake generic WKProto message sender for dry-run examples and tests.
- `identity.pool/v1`: produces deterministic user/device identities.
- `identity.person_pairs/v1`: produces deterministic person-channel send targets.
- `wukongim.target/v1`: describes and probes black-box WuKongIM endpoints.
- `wukongim.prepare_tokens/v1`: prepares user tokens through `/bench/v1/users/tokens`.
- `wukongim.prepare_group_channels/v1`: prepares group channels and subscribers through `/bench/v1`.
- `wkproto.session_pool/v1`: opens real WKProto sessions and provides legacy `port.wkproto.group_sender/v1` senders plus generic `port.wkproto.message_sender/v1` senders for `traffic.send/v1`.
- `traffic.group_send/v1`: sends group messages through `port.wkproto.group_sender/v1`.
- `traffic.send/v1`: sends protocol messages through `port.wkproto.message_sender/v1` and measures `SEND -> SENDACK` latency.
- `report.assert/v1`: asserts traffic summary values.

Validate the real WuKongIM example without connecting:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-group-send.yaml
```

Inspect the same scenario's graph without connecting:

```bash
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-group-send.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-group-send.yaml -format json
```

Plan the same scenario without connecting:

```bash
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-group-send.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-group-send.yaml -format json
```

Validate, inspect, and plan the mixed group/person send-rate scenario without connecting:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-send-rate-mixed.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-send-rate-mixed.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-send-rate-mixed.yaml
```

Run the single-node WuKongIM smoke after starting a target with bench API enabled:

```bash
./scripts/smoke-wukongim-single-node.sh
```

Run the mixed group/person send-rate smoke against the same target:

```bash
./scripts/smoke-wukongim-send-rate-mixed.sh
```

Override the scenario path for either smoke script when needed:

```bash
WKBENCH_SCENARIO=/path/to/scenario.yaml ./scripts/smoke-wukongim-single-node.sh
WKBENCH_SCENARIO=/path/to/scenario.yaml ./scripts/smoke-wukongim-send-rate-mixed.sh
```

Start a local WuKongIM v2 three-node target from this repository and run the mixed send-rate scenario against all three nodes:

```bash
./scripts/start-wukongimv2-three-nodes.sh --clean
GOWORK=off go run ./cmd/wkbench run -scenario ./examples/wukongim-three-node-send-rate-mixed.yaml
```

Sweep a three-node target to find the highest passing send-link QPS:

```bash
./scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 100,200,500 \
  --duration 2m \
  --no-start-target
```

In `mixed` mode, the script runs person and group sub-scenarios concurrently and reports both per-workload rows plus an aggregate `total` row.

For an end-to-end local run that starts and stops the three-node target:

```bash
./scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 10,20 \
  --duration 5s \
  --start-target \
  --clean-target
```

## Architecture Notes

- `benchkit/contract` defines the stable Unit API.
- `benchkit/ports/*` defines shared capability contracts.
- `benchkit/kernel` validates graph wiring, auto-connects unique matching ports, plans, and runs units.
- `cmd/wkbench` registers the built-in distribution.

See [docs/design/wkbench-v2-unit-architecture.md](docs/design/wkbench-v2-unit-architecture.md) and [docs/unit-standard.md](docs/unit-standard.md).
