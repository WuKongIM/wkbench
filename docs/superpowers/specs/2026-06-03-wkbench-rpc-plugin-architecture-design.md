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

The protocol is bidirectional. Host-to-plugin lifecycle calls are not enough,
because a running unit still needs the host-owned run environment for inputs,
outputs, artifacts, cancellation, and cross-plugin capability streams. The
transport must therefore support full-duplex multiplexing with request IDs,
run IDs, unit instance IDs, and deadline/cancellation messages.

Minimum host-to-plugin services:

```text
PluginService
  Handshake(HandshakeRequest) returns (HandshakeResponse)
  ListUnits(ListUnitsRequest) returns (ListUnitsResponse)
  Validate(ValidateRequest) returns (ValidateResponse)
  Plan(PlanRequest) returns (PlanResponse)
  Run(stream HostFrame) returns (stream PluginFrame)
  Start(StartRequest) returns (StartResponse)
  Stop(StopRequest) returns (StopResponse)
  CloseRun(CloseRunRequest) returns (CloseRunResponse)
```

Minimum plugin-to-host run environment services:

```text
RunEnvService
  GetInput(GetInputRequest) returns (PortValue)
  SetOutput(SetOutputRequest) returns (SetOutputResponse)
  FlushMetrics(FlushMetricsRequest) returns (FlushMetricsResponse)
  OpenArtifact(OpenArtifactRequest) returns (ArtifactWriteStream)
  OpenStreamCapability
  CloseCapability
  ReportBackgroundEvent
```

`Run` is always a bidirectional stream. The first host frame contains the
`RunRequest`; later host frames carry run-environment responses, cancellation,
and deadlines. Plugin frames carry run-environment requests, metric flushes,
outputs, artifact writes, capability stream frames, and terminal status. There
are no ad hoc side channels. Every frame carries the run/session ID and unit
instance ID so the host can correlate metrics, artifacts, outputs,
cancellation, and errors.

`Start` and `Stop` support background units. `Start` returns only after the
background task is ready for downstream units. A plugin returns a task handle
and then keeps reporting background events until `Stop` completes or the task
fails.

Background task events:

- `ready`: emitted before `Start` returns, after the worker is active.
- `heartbeat`: optional liveness signal for long-running tasks.
- `fatal_error`: cancels the run and marks the task's unit as failed.
- `completed`: task exited normally before or during `Stop`.

`Stop` must be bounded by a host deadline. If the plugin does not stop in time,
the host cancels the task, records a stop timeout, and escalates to process
termination after active units and artifacts have been snapshotted as far as
possible.

RPC responses should carry structured errors:

```text
code: CONFIG_ERROR | PLAN_ERROR | RUN_ERROR | INTERNAL_ERROR | CANCELED
message: human-readable summary
details: optional JSON/protobuf fields
```

The host maps those errors to the existing report statuses.

## Port Model

Ports must be explicit about whether they can cross a process boundary.
Every input and output port definition must declare enough metadata for the
host to validate wiring before execution:

```yaml
name: sender
type: port.wkproto.message_sender/v1
boundary: stream_capability
schema: wkbench.ports.wkproto.MessageSenderV1
encodings: [protobuf]
transport: inline
max_payload_bytes: 1048576
sensitive: false
reportable: false
operations:
  - OpenSendStream
```

Required metadata:

- `boundary`: `data`, `stream_capability`, or `local_resource`.
- `schema`: versioned data schema or capability contract.
- `encodings`: allowed wire encodings for data payloads.
- `transport`: `inline`, `paged`, or `artifact_ref` for data ports.
- `max_payload_bytes`: hard bound for inline data payloads.
- `sensitive`: whether payloads or fields need redaction and consent rules.
- `reportable`: whether a compact value may appear in `report.json`.
- `operations`: capability operations when `boundary` is `stream_capability`.

The host rejects scenarios that wire incompatible boundaries, schemas,
encodings, sensitivity rules, or capability operation contracts.

### Data Port

Data ports are JSON/protobuf-serializable and may be consumed by any plugin
when their sensitivity policy allows it.

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
sensitive: false
reportable: true
payload: bytes
```

The host validates declared port types and forwards payloads without needing to
understand every domain schema. It must still enforce the port metadata:
sensitive payloads are redacted in logs and reports, non-reportable payloads
cannot appear inline in `report.json`, and payloads over the inline size limit
must use `transport: paged`, `transport: artifact_ref`, or a local/capability
port.

Data transports:

- `inline`: one bounded payload carried in the output envelope.
- `paged`: host asks the producing plugin for deterministic pages by cursor or
  offset; page size and total size are declared in the output envelope.
- `artifact_ref`: output envelope points at a host-managed artifact for large
  generated datasets.

Concrete first-version limits:

- inline data port payloads default to 1 MiB unless the port schema declares a
  lower limit;
- reportable output summaries default to 64 KiB;
- larger identity, channel, or sample sets must be represented as data ports
  with `transport: paged` or `transport: artifact_ref`, not single inline
  payloads.

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

The host proxies stream capability traffic, but it should not inspect message
payloads on the hot path except for routing, accounting, and cancellation.

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
wkbench plugin lock
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
benchkit/ports/           public port schemas and capability contracts
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
source: github.com/acme/wkbench-plugin@v0.1.0
units:
  - kind: acme.target/v1
    plugin_unit_id: target
  - kind: acme.send_traffic/v1
    plugin_unit_id: send_traffic
```

Install locations:

```text
./.wkbench/plugins/       project-local plugins
~/.wkbench/plugins/       user plugins
bundled/                  official plugins shipped with wkbench
```

Resolution order should be project-local, then user, then bundled. Duplicate
plugin names with incompatible versions should fail before scenario validation.

Unit kind ownership must be deterministic:

- multiple plugins may expose the same unit kind, but unqualified `use:` is
  valid only when exactly one enabled plugin provides that kind;
- if two enabled plugins provide the same kind, unqualified `use:` is a
  scenario-time ambiguity error;
- a scenario disambiguates through explicit plugin ownership syntax, such as
  `use: acme.system:acme.send_traffic/v1`;
- reports record the resolved plugin name, plugin version, source, checksum,
  protocol version, and unit kind for every scenario unit.

Installed plugins should be reproducible through a lockfile:

```text
wkbench.plugin.lock
```

The lockfile records plugin name, version, source, checksum, protocol version,
and installed executable path. `wkbench run` should warn or fail, according to
strictness settings, when the resolved plugin set differs from the lockfile.
Even metadata-only commands such as `list-units` and `validate` execute plugin
binaries, so users need this explicit trust and provenance trail.

## Sensitive Data And Trust

Plugins are executable code. The host cannot make an untrusted plugin safe after
launching it, but it can make trust decisions explicit and prevent accidental
secret exposure.

Sensitive data rules:

- port schemas mark sensitive fields and sensitive whole-payload ports;
- sensitive values are redacted in host logs, plugin error details, reports,
  and scenario explanations;
- reportable outputs must be compact summaries with sensitive fields removed;
- a third-party plugin may receive a sensitive data port only when the scenario
  explicitly wires that input or declares the plugin as trusted for that run;
- automatic input wiring must not silently connect sensitive ports across
  plugin ownership boundaries;
- plugin stderr is captured as an artifact or log reference, but host-rendered
  summaries redact known sensitive values.

Trust should be scenario-visible:

```yaml
requires:
  plugins:
    - name: acme.system
      version: ">=0.1.0"
      trust: sensitive-inputs
```

If `trust` is omitted, the plugin can still consume non-sensitive data ports and
capabilities, but the host rejects implicit sensitive data wiring to it.

## Execution Semantics

Graph execution remains deterministic:

1. Collect the unit catalog from all required plugins.
2. Parse and expand scenario YAML.
3. Resolve each `use` kind to exactly one plugin unit.
4. Validate duplicate kind and plugin ownership rules.
5. Validate local specs by RPC.
6. Validate port wiring using unit definitions and port boundary rules.
7. Plan each unit by RPC in graph order.
8. Run foreground units and start background units in graph order.
9. Watch background event streams while foreground work runs.
10. Stop background tasks in reverse start order with deadlines.
11. Close capabilities and local resources.
12. Write report artifacts.

The host owns graph state. Plugins own unit internals.

## Metrics And Artifacts

Metrics should remain host-owned at the report boundary, but plugins should
aggregate metrics locally. A unit must not call the host for every benchmark
message on a hot path.

Plugin-side metric recording APIs should look like local SDK calls:

```text
EmitCounter
ObserveDuration
EmitGauge
```

The SDK aggregates those calls inside the plugin process. The plugin flushes
bounded metric snapshots to the host periodically and at unit completion:

```text
FlushMetrics
  counters: delta sums since last flush
  gauges: latest values
  durations: count, sum, min, max, and bounded histogram/sketch buckets
```

Flush rules:

- hot traffic units aggregate per-message observations locally;
- flush interval defaults to one second for long-running units;
- units always flush once before `Run` returns or `Stop` completes;
- duration metrics use bounded histograms or sketches, not raw unbounded
  samples;
- the host merges snapshots into deterministic report summaries.

This preserves one report model without making RPC overhead part of the
measured workload.

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

Compatibility policy:

- plugin protocol versions support an explicit min/max range in handshake;
- patch versions must be wire-compatible;
- minor versions may add optional fields, operations, and unit kinds;
- breaking protocol, unit spec, or port schema changes require a new versioned
  identifier;
- official plugins should keep at least one previous minor line loadable while
  examples and docs migrate;
- deprecated protocol, unit, or port versions should emit warnings during
  `validate` before removal in a later release.

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
- data port wiring and paged transport across plugins
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

Additional protocol tests:

- bidirectional run environment calls cannot deadlock during `Run`
- background fatal events cancel foreground execution
- plugin-side metric aggregation does not flush per message
- sensitive ports are not auto-wired across plugin ownership boundaries
- plugin lockfile mismatch is reported before execution

## Current Port Inventory

The rebuild starts by classifying existing ports. This inventory should be
expanded as ports change, but the initial classification is:

```text
port.identity.pool/v1
  boundary: data
  transport: inline for small pools, paged for large pools
  sensitive: true when identities include tokens
  notes: current Go interface must become a bounded/pageable schema.

port.identity.token_source/v1
  boundary: local_resource by default; data only with explicit trust
  transport: inline when exposed as data
  sensitive: true
  notes: avoid automatic cross-plugin wiring; token lookup may become an
  explicit sensitive capability if cross-plugin use is required.

port.channel.group_set/v1
  boundary: data
  transport: inline for small sets, paged for large sets
  sensitive: false by default
  notes: large group sets should not be sent as one inline payload.

port.channel.send_target_set/v1
  boundary: data
  transport: inline for small sets, paged for large sets
  sensitive: false by default
  notes: sender UID lists may be large and need paging.

port.target.endpoint/v1
  boundary: data
  sensitive: true when BenchAPIToken is present
  notes: token field must be redacted from reports, errors, and logs.

port.wkproto.message_sender/v1
  boundary: stream_capability or local_resource
  sensitive: false payload, but may access sensitive session state
  notes: cross-plugin form must be `OpenSendStream`, not per-message unary RPC.

port.wkproto.group_sender/v1
  boundary: stream_capability or local_resource
  sensitive: false payload, but may access sensitive session state
  notes: can share the generic message stream operation with channel type.

port.traffic.summary/v1
  boundary: data
  sensitive: false
  reportable: true
  notes: compact JSON-friendly output.

port.wukongim.metrics_summary/v1
  boundary: data
  sensitive: false by default
  reportable: true
  notes: keep raw scrape samples in artifacts.
```

## Migration Plan

Because this is a young project, prefer a direct rebuild over long-lived
compatibility layers.

1. Inventory every current port and classify it as data, stream capability,
   local resource, and sensitive/non-sensitive.
2. Define protobuf schemas and capability operation contracts for each public
   port that survives the rebuild.
3. Add the bidirectional protobuf protocol and `pluginhost`.
4. Add the Go SDK and test plugin harness.
5. Rebuild the CLI around plugin catalogs instead of in-process registries.
6. Move core fake/static units into an official core plugin.
7. Move traffic units into an official traffic plugin.
8. Move WuKongIM target/session/preparation/collector units into an official
   WuKongIM plugin.
9. Move report assertion units into an official report plugin.
10. Delete the old CLI unit registration path.
11. Update docs, examples, and scaffolding for plugin-first authoring.

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
