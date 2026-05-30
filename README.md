# wkbench

`wkbench` is a composable benchmark toolkit for WuKongIM and related black-box messaging workloads.

This repository starts the v2 architecture: a small kernel runs scenario graphs, while independent units provide capabilities through versioned ports. Units do not import each other. Composition happens only in scenario YAML.

## Quick Start

Run the dry-run group SEND example:

```bash
GOWORK=off go run ./cmd/wkbench run -scenario ./examples/group-send.yaml
```

List built-in units:

```bash
GOWORK=off go run ./cmd/wkbench list-units
```

Run tests:

```bash
GOWORK=off go test ./...
```

## Current Built-In Units

- `core.static_groups/v1`: produces deterministic in-memory group channels.
- `core.fake_group_sender/v1`: produces a fake WKProto group sender for examples and tests.
- `identity.pool/v1`: produces deterministic user/device identities.
- `wukongim.target/v1`: describes and probes black-box WuKongIM endpoints.
- `wukongim.prepare_tokens/v1`: prepares user tokens through `/bench/v1/users/tokens`.
- `wukongim.prepare_group_channels/v1`: prepares group channels and subscribers through `/bench/v1`.
- `wkproto.session_pool/v1`: opens real WKProto sessions and provides group senders.
- `traffic.group_send/v1`: sends group messages through `port.wkproto.group_sender/v1`.
- `report.assert/v1`: asserts traffic summary values.

Validate the real WuKongIM example without connecting:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-group-send.yaml
```

## Architecture Notes

- `benchkit/contract` defines the stable Unit API.
- `benchkit/ports/*` defines shared capability contracts.
- `benchkit/kernel` validates graph wiring, auto-connects unique matching ports, plans, and runs units.
- `cmd/wkbench` registers the built-in distribution.

See [docs/design/wkbench-v2-unit-architecture.md](docs/design/wkbench-v2-unit-architecture.md) and [docs/unit-standard.md](docs/unit-standard.md).
