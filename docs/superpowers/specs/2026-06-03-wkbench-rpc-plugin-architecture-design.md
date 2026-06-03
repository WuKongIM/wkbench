# wkbench RPC Plugin Architecture Design

## Goal

Rebuild `wkbench` around a single plugin execution model. The `wkbench`
binary should be a benchmark host: it parses scenarios, discovers units from
plugins, validates graph wiring, orchestrates execution, and writes reports.
All benchmark behavior should live in plugins, including official WuKongIM
units.

This removes the current requirement that third-party units be committed into
the main repository and compiled into the official binary.

## Non-Goals

- Do not preserve the old in-process unit registry as a second execution path.
- Do not support raw Go object outputs across unit boundaries.
- Do not make the hot traffic path perform one host RPC call per benchmark
  message.
- Do not require third-party unit authors to fork or patch the `wkbench`
  repository.
- Do not make the first implementation support every possible language SDK.
  The protocol should allow other languages later, but the first SDK can be Go.

## Current State

The current architecture already has a clean graph/kernel boundary, but the CLI
is still a distribution binary that imports units directly:

```text
cmd/wkbench
  -> benchkit/kernel
  -> benchkit/registry
  -> units/*
```

`benchkit/kernel` does not import `units/*`, which is good. The coupling exists
at registration time in `cmd/wkbench`. Third-party units therefore need to be
compiled into a `wkbench` binary to be usable.

The current unit interface is Go-native:

```go
type Unit interface {
    Definition() Definition
    Validate(context.Context, ValidateEnv) error
    Plan(context.Context, PlanEnv) (Plan, error)
    Run(context.Context, RunEnv) error
}
```

That shape is ergonomic for Go unit authors, but it cannot be exposed directly
across process boundaries because ports can contain interfaces, clients,
connection pools, or other runtime resources.

## Recommended Architecture

Use one execution model for both official and third-party units:

```text
wkbench host process
  -> scenario parser
  -> plugin catalog
  -> graph engine
  -> plugin RPC clients
  -> report writer

plugin process
  -> unit implementations
  -> target clients, pools, workers, collectors
  -> local resource registry
```

The host never imports unit packages. It starts plugin processes, asks them for
their unit definitions, builds a scenario graph from the combined catalog, and
then calls plugin RPC methods for unit lifecycle operations.

Official units should move from `units/*` into official plugins such as:

```text
plugins/official/core
plugins/official/traffic
plugins/official/wukongim
plugins/official/report
```

Those official plugins are distributed with `wkbench` by default, but they still
run through the same RPC protocol as third-party plugins.

## Process Model

Each plugin is an executable. The host launches it with a controlled environment
and communicates over stdio, Unix socket, or localhost TCP. Stdio is simplest
for installation and process cleanup; Unix socket or localhost TCP is better
for debugging and multiplexed streams. The protocol should hide transport
choice behind `pluginhost`.

Plugin lifecycle:

1. Host discovers plugin manifests from the workspace and user plugin dirs.
2. Host starts required plugin processes for a command.
3. Host sends a handshake request with host protocol version and run metadata.
4. Plugin responds with plugin metadata, supported protocol range, and units.
5. Host validates unit kinds, port types, and version compatibility.
6. Host calls `Validate`, `Plan`, `Run`, `Start`, and `Stop` as needed.
7. Host asks plugins to close run resources and then terminates plugin
   processes.

For commands that only need static metadata, such as `list-units`, the host may
start plugins briefly and exit after catalog collection.

## RPC Protocol

Use a versioned protobuf protocol as the stable ABI. Connect, gRPC, or a small
custom framed transport can carry the protobuf messages. The important point is
that the ABI is protobuf, not Go interfaces.

Minimum services:

```text
PluginService
  Handshake(HandshakeRequest) returns (HandshakeResponse)
  ListUnits(ListUnitsRequest) returns (ListUnitsResponse)
  Validate(ValidateRequest) returns (ValidateResponse)
  Plan(PlanRequest) returns (PlanResponse)
  Run(RunRequest) returns (RunResponse)
  Start(StartRequest) returns (StartResponse)
  Stop(StopRequest) returns (StopResponse)
  CloseRun(CloseRunRequest) returns (CloseRunResponse)

ArtifactService
  OpenArtifact/OpenArtifactStream

CapabilityService
  OpenStreamCapability
  CloseCapability
```

`Start` and `Stop` support background units. A plugin can return a task handle
from `Start`. The host stores that handle and passes it back to `Stop`.

RPC responses should carry structured errors:

```text
code: CONFIG_ERROR | PLAN_ERROR | RUN_ERROR | INTERNAL_ERROR | CANCELED
message: human-readable summary
details: optional JSON/protobuf fields
```

The host maps those errors to the existing report statuses.

## Port Model

Ports must be explicit about whether they can cross a process boundary.

### Data Port

Data ports are JSON/protobuf-serializable and may be consumed by any plugin.

Examples:

- target config
- identity list
- group list
- traffic summary
- assertion result
- metrics summary

Data port values are sent as an envelope:

```text
type: port.identity.pool/v1
encoding: json | protobuf
schema_version: v1
payload: bytes
```

The host validates declared port types and forwards payloads without needing to
understand every domain schema.

### Stream Capability Port

Stream capability ports represent remote behavior that may cross plugin
boundaries. They are not raw Go interfaces. They are remote capability
references with a versioned operation contract.

Examples:

- `port.wkproto.message_sender/v1`
- `port.wkproto.group_sender/v1`

A provider returns:

```text
type: port.wkproto.message_sender/v1
capability_ref: plugin-run-id/resource-id
operations:
  - OpenSendStream
```

The consuming plugin opens a stream through the host. The host proxies the
stream to the provider plugin. Traffic messages flow over that stream in batches
or as a long-lived duplex stream. This avoids one host RPC round trip per send.

The stream protocol should support:

- batched send requests
- ordered or correlated acknowledgments
- backpressure
- deadline/cancellation
- final stats and terminal error

### Local Resource Port

Local resource ports are plugin-private. They may connect units within the same
plugin, but cannot be consumed by another plugin.

Examples:

- raw TCP client
- session pool
- file handle
- target-specific SDK object

The plugin returns a local resource reference. The host can wire it only when
the producer and consumer belong to the same plugin process. If a scenario tries
to wire a local resource across plugins, validation fails with a clear error.

This category keeps high-performance and target-specific internals out of the
host ABI.

## Scenario Experience

Scenario YAML should remain focused on composition:

```yaml
version: wkbench/v2

requires:
  plugins:
    - name: wkbench.core
      version: ">=0.1.0"
    - name: acme.system
      version: ">=0.1.0"

run:
  id: acme-send
  duration: 30s
  report_dir: ./reports/acme-send

units:
  users:
    use: core.identity_pool/v1
    spec:
      count: 1000

  target:
    use: acme.target/v1
    spec:
      addr: 127.0.0.1:5000

  traffic:
    use: acme.send_traffic/v1
    inputs:
      identities: users.identities
      sender: target.sender
    spec:
      rate: 10000/s
      payload_size: 128
```

The user experience:

```bash
wkbench plugin install github.com/acme/wkbench-plugin@v0.1.0
wkbench plugin list
wkbench list-units
wkbench validate -scenario ./bench.yaml
wkbench run -scenario ./bench.yaml
```

The user does not need to rebuild `wkbench` or copy unit code into this
repository.

## Plugin Author Experience

The first-class authoring path should be a Go SDK:

```bash
wkbench plugin new github.com/acme/wkbench-plugin
cd wkbench-plugin
wkbench unit new --kind acme.target/v1 --dir ./units/target
wkbench unit new --kind acme.send_traffic/v1 --dir ./units/send_traffic
go test ./...
wkbench plugin build
```

Plugin entrypoint:

```go
func main() {
    wkplugin.Serve(wkplugin.Plugin{
        Name:    "acme.system",
        Version: "0.1.0",
        Units: []wkplugin.Unit{
            target.Unit{},
            sendtraffic.Unit{},
        },
    })
}
```

The SDK should keep the author-facing unit shape close to the current one, but
the SDK translates between Go methods and RPC messages.

Author-facing APIs:

```go
type Unit interface {
    Definition() Definition
    Validate(context.Context, ValidateEnv) error
    Plan(context.Context, PlanEnv) (Plan, error)
    Run(context.Context, RunEnv) error
}

type BackgroundUnit interface {
    Unit
    Start(context.Context, RunEnv) (BackgroundTask, error)
}
```

Output APIs must force authors to declare the boundary type:

```go
env.SetDataOutput("summary", traffic.SummaryV1, summary)
env.SetStreamCapability("sender", wkproto.MessageSenderV1, sender)
env.SetLocalResource("pool", wkproto.SessionPoolLocalV1, pool)
```

This prevents accidental exposure of raw Go objects.

## Directory Layout

Target layout after the rebuild:

```text
cmd/wkbench/              host CLI only
benchkit/dsl/             scenario YAML parsing
benchkit/engine/          graph validation, planning, execution
benchkit/pluginhost/      discovery, process lifecycle, RPC clients
benchkit/protocol/        generated protobuf and versioned ABI helpers
benchkit/report/          report writer
ports/                    public port schemas and capability contracts
sdk/go/wkbench/           Go plugin authoring SDK
sdk/go/wkbench/plugin/    Serve, manifests, test harnesses
plugins/official/core/    official core plugin
plugins/official/traffic/ official traffic plugin
plugins/official/wukongim/ official WuKongIM plugin
plugins/official/report/  official report/assert plugin
examples/                 scenarios
docs/                     architecture and authoring docs
```

The old `benchkit/contract` can become SDK-facing API. The host-facing runtime
contract should move into `benchkit/protocol` and `benchkit/engine`.

## Plugin Manifests

Each plugin should have a manifest embedded in the binary and optionally stored
next to it:

```yaml
name: acme.system
version: 0.1.0
protocol: wkbench.plugin/v1
units:
  - kind: acme.target/v1
  - kind: acme.send_traffic/v1
```

Install locations:

```text
./.wkbench/plugins/       project-local plugins
~/.wkbench/plugins/       user plugins
bundled/                  official plugins shipped with wkbench
```

Resolution order should be project-local, then user, then bundled. Duplicate
plugin names with incompatible versions should fail before scenario validation.

## Execution Semantics

Graph execution remains deterministic:

1. Collect the unit catalog from all required plugins.
2. Parse and expand scenario YAML.
3. Resolve each `use` kind to exactly one plugin unit.
4. Validate local specs by RPC.
5. Validate port wiring using unit definitions and port boundary rules.
6. Plan each unit by RPC in graph order.
7. Run foreground units and start background units in graph order.
8. Stop background tasks in reverse start order.
9. Close capabilities and local resources.
10. Write report artifacts.

The host owns graph state. Plugins own unit internals.

## Metrics And Artifacts

Metrics should remain host-collected so reports are consistent. Plugins emit
metrics through the RPC run environment:

```text
EmitCounter
ObserveDuration
EmitGauge
```

For high-volume raw samples, plugins should write artifacts through host-managed
artifact streams. Large raw samples should not be placed inline in `report.json`.

Artifact rules remain:

- artifact names must be declared in the unit definition
- artifact names must be simple relative file names
- artifacts are written under the run report directory
- metadata appears in `report.json`

## Performance Rules

The architecture must protect benchmark validity:

- Hot message traffic should stay inside one plugin whenever possible.
- Cross-plugin hot paths must use streaming capability ports.
- The host should not inspect every benchmark message.
- Data port payloads should be bounded and report-friendly.
- Large samples go to artifacts, not outputs.
- Capability streams should expose backpressure and batching.

The default official WuKongIM scenario should keep session pools and send
traffic inside the same official plugin unless a scenario explicitly composes a
cross-plugin stream.

## Compatibility And Versioning

Versioned layers:

- plugin protocol version, for host/plugin RPC compatibility
- SDK version, for Go authoring ergonomics
- unit kind version, for unit spec compatibility
- port type version, for data and capability compatibility

Handshake must negotiate protocol compatibility before units are listed. Unit
and port compatibility are validated during scenario loading.

Breaking changes require new versions. Existing versions should remain
loadable until deliberately removed from the distribution.

## Error Handling

Errors should be structured and mapped to run statuses:

- invalid scenario or spec: `config_failed`
- deterministic planning failure: `plan_failed`
- runtime unit failure: `worker_failed`
- plugin crash: `worker_failed` with plugin process metadata
- protocol mismatch: `config_failed`
- host internal bug: `internal_failed`

When a plugin crashes, the host should:

1. mark active units owned by that plugin as failed,
2. cancel downstream execution,
3. stop other active background tasks,
4. preserve partial metrics and artifacts where possible,
5. include plugin stderr/log path in the report.

## Testing Strategy

Host tests:

- plugin discovery and version resolution
- duplicate unit kind handling
- data port wiring across plugins
- local resource wiring rejection across plugins
- stream capability wiring and cancellation
- plugin crash behavior
- background start/stop ordering
- report rendering with plugin-owned units

SDK tests:

- generated plugin serves definitions correctly
- unit validation, plan, run, metrics, artifacts
- data output encoding
- capability stream implementation
- local resource lifecycle cleanup

Official plugin tests:

- unit contract tests in plugin packages
- scenario validation using official plugin binaries
- dry-run examples
- smoke scripts for live WuKongIM targets

End-to-end tests:

- build official plugins
- run `wkbench list-units`
- validate example scenarios
- run dry scenarios without a live WuKongIM target

## Migration Plan

Because this is a young project, prefer a direct rebuild over long-lived
compatibility layers.

1. Add the protobuf protocol and `pluginhost`.
2. Add the Go SDK and test plugin harness.
3. Rebuild the CLI around plugin catalogs instead of in-process registries.
4. Move core fake/static units into an official core plugin.
5. Move traffic units into an official traffic plugin.
6. Move WuKongIM target/session/preparation/collector units into an official
   WuKongIM plugin.
7. Move report assertion units into an official report plugin.
8. Delete the old CLI unit registration path.
9. Update docs, examples, and scaffolding for plugin-first authoring.

During migration, temporary adapters are acceptable inside tests and migration
branches, but the final architecture should have one host-to-plugin execution
path.

## Success Criteria

- A third-party author can publish a plugin without modifying this repository.
- A user can install that plugin and use its units from scenario YAML.
- Official units run through the same plugin protocol as third-party units.
- Data ports can cross plugin boundaries.
- Local resources cannot accidentally cross plugin boundaries.
- Streaming capabilities support high-throughput cross-plugin use cases without
  one RPC call per benchmark message.
- Reports remain deterministic, compact, and JSON-friendly.
- The host binary has no imports from official or third-party unit packages.
