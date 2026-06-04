# wkbench Remote Background Lifecycle Design

## Goal

Phase 2D-A adds plugin RPC support for `contract.BackgroundUnit` so long-running
collector units can run out-of-process while the kernel keeps its existing
background scheduling semantics.

The first migrated unit is `wukongim.metrics_collector/v1`. Stream capability
ports, WKProto session pools, traffic hot paths, fake senders, and token-source
interfaces remain host-local until a separate stream capability phase.

## Current State

The plugin protocol supports `Handshake`, `Validate`, `Plan`, and `Run`.
Remote `Run` already streams outputs, aggregate metrics, and host-managed
artifacts over the same stdio frame channel.

The kernel supports background units in-process by detecting
`contract.BackgroundUnit`, calling `Start`, monitoring `BackgroundTask.Done`,
and calling `Stop` after foreground work completes. Background units publish
outputs, metrics, and artifacts into the same `RunEnv` used by normal units.

`wukongim.metrics_collector/v1` is the only current official background unit.
Its `Run` method intentionally fails, and its `Start` method opens a
host-managed artifact, starts a scraper worker, emits metrics, and publishes a
summary during `Stop`.

## Non-Goals

- Do not implement stream capability ports.
- Do not migrate `traffic.*`, `wkproto.session_pool/v1`, fake senders, or
  `wukongim.prepare_tokens/v1`.
- Do not add one-RPC-per-message hot paths.
- Do not replace the kernel background scheduler.
- Do not require scenario YAML changes.

## Recommended Architecture

Add remote background lifecycle as a narrow extension of the existing plugin
RPC:

```text
kernel
  -> detects contract.BackgroundUnit
  -> pluginhost.RemoteUnit.Start
  -> pluginhost.StdioClient.Start
  -> protocol StartRequest frame
  -> sdk plugin server calls unit.Start
  -> returns StartResponse with task_id

kernel later calls task.Stop
  -> pluginhost remote task Stop
  -> protocol StopRequest frame
  -> sdk plugin server calls BackgroundTask.Stop
  -> flushes outputs, metrics, artifacts
  -> StopResponse / terminal status
```

The remote background task uses the same `request_id` for artifact, output, and
metric frames associated with the background unit. The SDK server keeps the
plugin-side `BackgroundTask` and its `remoteRunEnv` in a task registry keyed by
`task_id`.

The host-side remote task implements `contract.BackgroundTask`. Its `Done`
channel receives a fatal error when the plugin reports one. In Phase 2D-A the
only required fatal path is process/RPC failure or an explicit background event
from the plugin server when a task's `Done` channel returns a non-nil error.

## Protocol Changes

Extend `benchkit/protocol/wkbench_plugin.proto` with these frame bodies:

```proto
StartRequest start_request = 30;
StartResponse start_response = 31;
StopRequest stop_request = 32;
StopResponse stop_response = 33;
BackgroundEvent background_event = 34;
```

Messages:

```proto
message StartRequest {
  string unit_name = 1;
  string kind = 2;
  string run_id = 3;
  int64 run_duration_millis = 4;
  int32 worker_count = 5;
  bytes spec_json = 6;
  map<string, PortValue> inputs = 7;
}

message StartResponse {
  string task_id = 1;
}

message StopRequest {
  string task_id = 1;
}

message StopResponse {}

message BackgroundEvent {
  string task_id = 1;
  string event = 2;
  Error error = 3;
}
```

`event` values:

- `fatal_error`: the background task failed before or during foreground work.
- `completed`: the task exited without error before `Stop`.

`StartRequest` mirrors `RunRequest` because a background unit needs the same
spec, run metadata, and JSON inline inputs. Phase 2D-A keeps the existing
Phase 1 boundary checks: only non-sensitive inline data inputs can cross the
plugin boundary.

## Host Changes

`benchkit/pluginhost.Client` gains:

```go
Start(context.Context, StartRequest, contract.RunEnv) (RemoteBackgroundTask, error)
```

`StartRequest` reuses the current `RunRequest` shape. A returned
`RemoteBackgroundTask` implements `contract.BackgroundTask`.

`pluginhost.NewRemoteUnit` and `NewRemoteUnitAlias` return a background wrapper
when the manifest marks the unit as background-capable. The base
`pluginhost.RemoteUnit` itself must not implement `contract.BackgroundUnit`,
because Go's structural interfaces would otherwise make every remote unit look
background-capable. The wrapper's `Start` encodes the spec, collects inputs,
validates input source metadata, and calls the client. Normal `Run` behavior
stays unchanged.

`pluginhost.StdioClient` handles background frames on the same stdio stream.
To support asynchronous background failures while foreground local units run,
the client owns one reader goroutine that continuously reads plugin frames and
demultiplexes them by `request_id` and `task_id`:

- `Start` writes `StartRequest` and waits for `StartResponse`.
- It creates a host-side task with a `Done` channel and stores it by `task_id`.
- `Stop` writes `StopRequest`, then reads frames until `StopResponse`.
- `SetOutput`, `MetricFlush`, and artifact frames during stop are applied to
  the original host `RunEnv`.
- `BackgroundEvent(fatal_error)` completes the host task's `Done` channel with
  an error.

Writes remain serialized by the existing IO mutex. Reads must not be performed
directly by individual lifecycle calls after the read pump is introduced;
otherwise a synchronous call can steal an asynchronous `BackgroundEvent` or two
goroutines can read from the same frame stream. Existing `Handshake`,
`Validate`, `Plan`, and `Run` calls should wait on per-request frame channels
owned by the pump.

## SDK Server Changes

The Go plugin server gains `handleStart` and `handleStop`:

- `handleStart` verifies the unit implements `contract.BackgroundUnit`.
- It decodes inputs the same way `handleRun` does.
- It creates `remoteRunEnv`, calls `Start`, stores the returned task and env,
  and returns a generated `task_id`.
- It monitors `task.Done()` in a goroutine and emits `BackgroundEvent` when the
  task exits before stop.
- `handleStop` calls `task.Stop`, writes any outputs, metrics, and artifact
  close acknowledgements already produced through the env, removes the task,
  and returns `StopResponse`.

The server must not call a background unit's `Run` method. If a non-background
unit receives `Start`, it returns a `CONFIG_ERROR`.

## Manifest Changes

`UnitDefinition` needs a boolean:

```proto
bool background = 8;
```

`pluginhost.Unit` mirrors this as `Background bool`. `ManifestFromUnits`
sets it when a unit implements `contract.BackgroundUnit`.

The CLI default official plugin loader can then register a remote unit that
also satisfies `contract.BackgroundUnit`. Local-only units are unaffected.

## Official Plugin Migration

After protocol support lands, `plugins/official/wukongim` includes:

- `wukongim.target/v1`
- `wukongim.prepare_group_channels/v1`
- `wukongim.metrics_collector/v1`

`cmd/wkbench.defaultRegistry` removes the direct
`wukongim.metrics_collector/v1` registration. It keeps:

- `core.fake_group_sender/v1`
- `core.fake_message_sender/v1`
- `traffic.group_send/v1`
- `traffic.send/v1`
- `wkproto.session_pool/v1`
- `wukongim.prepare_tokens/v1`

This keeps all capability and token-source boundaries local while proving that
background lifecycle can cross the plugin boundary.

## Error Handling

Start errors map to the kernel's existing `unit "<name>" start` failure path
because the remote unit returns an error from `Start`.

Fatal background events complete `Done` with an error. The existing kernel
monitor cancels foreground work and records the background failure.

Stop errors are returned from the remote task's `Stop` method. The existing
kernel shutdown path records stop failures and snapshots partial outputs,
metrics, and artifacts.

If the plugin process exits while a remote background task is active, the host
task's `Done` channel receives an error. The run fails as a background worker
failure.

## Testing Strategy

Protocol and host tests:

- manifest conversion preserves the `background` flag;
- `NewRemoteUnit` returns a wrapper implementing `contract.BackgroundUnit` only
  for background manifests, while base `RemoteUnit` remains non-background;
- `StdioClient.Start` and `Stop` can run a remote background test unit;
- fatal background events cancel the host task `Done` channel with an error;
- start rejects non-background units;
- stop flushes outputs, metrics, and artifacts.

SDK tests:

- `handleStart` calls `Start`, not `Run`;
- `handleStop` calls `Stop` and removes the task;
- task `Done` errors are emitted as `BackgroundEvent(fatal_error)`.

CLI/scenario tests:

- `list-units` still shows `wukongim.metrics_collector/v1` by default;
- `-no-official-plugins list-units` no longer shows
  `wukongim.metrics_collector/v1`;
- `validate/explain/plan` pass for `examples/wukongim-send-rate-with-metrics.yaml`;
- a local test server smoke scenario runs remote `metrics_collector` with a
  short foreground fake traffic unit and writes metrics artifact/report output.

Full verification:

```bash
GOWORK=off go test ./...
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-send-rate-with-metrics.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-send-rate-with-metrics.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-send-rate-with-metrics.yaml
```

## Later Phases

Phase 2D-B should design stream capability RPC for `port.wkproto.message_sender/v1`
and `port.wkproto.group_sender/v1`. That phase should batch sends and acks or
use a long-lived duplex stream so the benchmark hot path is not dominated by
host/plugin round trips.
