# wkbench Background Units and Metrics Collection Design

## Goal

Add a first-class background unit lifecycle to `wkbench` so long-running collectors can run at the same time as foreground workload units.

The first consumer should be a WuKongIM metrics collector that starts after the target is ready, scrapes every configured interval, continues while traffic runs, and flushes its samples and summary when the benchmark ends.

The design should answer two questions cleanly:

- How does `wkbench` run a unit concurrently without turning every unit into a scheduler?
- How do we collect service metrics without inflating `report.json` or corrupting workload timing?

## Non-Goals

- Do not introduce a distributed coordinator in this phase.
- Do not turn `tick` into a separate unit type. Tick is a behavior inside a background unit.
- Do not add a Prometheus server, remote write support, or a time-series database.
- Do not parse and aggregate every Prometheus metric into kernel metrics by default.
- Do not change WuKongIM server behavior.
- Do not make metrics scrape failures fail a workload by default.
- Do not require existing units to implement new methods.

## Current State

`contract.Unit` is a single-shot lifecycle:

```go
type Unit interface {
    Definition() Definition
    Validate(context.Context, ValidateEnv) error
    Plan(context.Context, PlanEnv) (Plan, error)
    Run(context.Context, RunEnv) error
}
```

`kernel.Engine.Run` executes units in topological order. For each unit it calls `Validate`, `Plan`, `Run`, then immediately snapshots that unit's outputs and metrics into `UnitResult`.

That means a collector can be written today, but neither available option is correct:

- If `Run` loops and scrapes every second, downstream traffic units never start.
- If `Run` starts a goroutine and returns, the kernel snapshots metrics before later samples arrive.

`CloseableOutput` helps release resources after the run, but it does not solve reporting because cleanup happens after `UnitResult` has already been snapshotted.

## Recommended Approach

Add an optional background lifecycle interface. Existing units remain unchanged. The kernel detects this interface and starts the unit without blocking later graph nodes.

```go
type BackgroundUnit interface {
    Unit
    Start(context.Context, RunEnv) (BackgroundTask, error)
}

type BackgroundTask interface {
    Done() <-chan error
    Stop(context.Context) error
}
```

Kernel behavior:

1. Build and validate the graph exactly as today.
2. Traverse graph order exactly as today.
3. For a normal unit, run and snapshot it exactly as today.
4. For a background unit, call `Start`, keep its `RunEnv` and `BackgroundTask` active, and continue to the next graph node.
5. If a background task reports a non-nil error on `Done`, cancel the run context and stop active background tasks.
6. When all foreground units finish, stop active background tasks in reverse start order.
7. Snapshot background unit outputs, metrics, artifacts, and timeline after `Stop` returns.
8. Run normal output cleanup after all foreground and background results have been recorded.

`Start` must return only after the background unit is ready. For a collector, that means its worker goroutine is running and any required output handles have been published. Downstream units can depend on the collector with `after: [metrics]` when they need it to be active before they start.

`Stop` must be idempotent. It should signal the worker to stop, wait for it to flush, close files, publish final outputs, and return any finalization error.

`Done` must eventually close. It may return:

- `nil` when the task exits normally.
- a non-nil error when the task considers itself fatal.

The background unit, not the kernel, decides whether an internal event is fatal. For example, the metrics collector can keep scrape errors as samples by default, or return a fatal error when configured with strict scrape failure rules.

## Execution Semantics

### Graph Order

Background units use the existing graph model:

```yaml
metrics:
  use: wukongim.metrics_collector
  after: [target]
  inputs:
    target: target.target

traffic:
  use: traffic.send
  after: [metrics]
```

`after: [target]` makes the collector start after WuKongIM readiness checks. `after: [metrics]` makes traffic start after the collector has started. The collector then continues until the whole run ends.

### Cancellation

The kernel should create a child context for the run. It passes that context to foreground and background units. If any foreground unit fails or any background task reports a fatal error, the kernel cancels the child context, stops every active background task, snapshots whatever was collected, and returns a failed run result.

### Result Status

Background unit result statuses:

- `completed`: `Start` succeeded, `Stop` succeeded, and `Done` did not report a fatal error.
- `worker_failed`: `Start`, `Done`, or `Stop` returned an error.
- `config_failed` and `plan_failed`: same meaning as normal units.

If a background unit fails after it has started, the overall run status should be `worker_failed`.

### Timeline

Add timing fields to `UnitResult`:

```go
StartedAt  string `json:"started_at,omitempty"`
EndedAt    string `json:"ended_at,omitempty"`
ElapsedMS  int64  `json:"elapsed_ms,omitempty"`
```

Normal units record `Run` start and end. Background units record `Start` time and final `Stop` completion time. This makes reports explain whether metrics covered the whole traffic window.

The run result should also gain optional run-level timing fields using the same format.

## Artifact Support

`ArtifactDef` exists but is not currently wired into runtime reporting. Background collectors need artifacts because raw samples can be large.

Add artifact metadata to `UnitResult`:

```go
type ArtifactResult struct {
    Path        string `json:"path"`
    ContentType string `json:"content_type,omitempty"`
    SizeBytes   int64  `json:"size_bytes,omitempty"`
}
```

Add a map on `UnitResult`:

```go
Artifacts map[string]ArtifactResult `json:"artifacts,omitempty"`
```

Extend `RunEnv` with an artifact creation API:

```go
OpenArtifact(name string) (io.WriteCloser, error)
```

Rules:

- `name` must match a declared artifact in `Definition().Artifacts`.
- `name` must be a simple relative file name with no path traversal.
- Artifacts are written under `run.report_dir/artifacts/<unit>/<name>`.
- If `run.report_dir` is empty and a unit opens an artifact, the kernel returns a clear error.
- The kernel records artifact metadata after the unit completes or the background task stops.

This keeps raw time-series data out of `report.json` while still making it discoverable.

## WuKongIM Metrics Collector

Add `wukongim.metrics_collector/v1`.

### Inputs

```go
Inputs:
  target: port.target.target/v1
```

### Outputs

```go
Outputs:
  summary: port.wukongim.metrics_summary/v1
```

The summary output should be reportable and compact.

### Artifacts

```go
Artifacts:
  metrics.jsonl
```

Each JSONL line is one scrape result for one target address.

### Spec

```yaml
interval: 1s
timeout: 800ms
path: /metrics
include:
  - "wk_.*"
  - "wukongim_.*"
exclude:
  - "go_.*"
  - "process_.*"
fail_on_scrape_error: false
max_consecutive_errors: 0
max_summary_metrics: 100
```

Field behavior:

- `interval`: scrape cadence. Must be greater than zero.
- `timeout`: per-address HTTP timeout. Defaults to the target operation timeout when omitted.
- `path`: defaults to `/metrics`.
- `include`: optional regular expressions. Empty means include all metric names before exclusion.
- `exclude`: optional regular expressions applied after include.
- `fail_on_scrape_error`: when false, scrape errors are counted and written to the artifact but do not fail the run.
- `max_consecutive_errors`: when greater than zero and `fail_on_scrape_error` is true, the collector reports a fatal error after that many consecutive failed ticks.
- `max_summary_metrics`: upper bound for metric names included in the compact reportable summary.

### Scrape Model

On each tick, the collector concurrently scrapes every `target.APIAddrs` entry.

Each address result records:

- timestamp
- node index
- address
- scrape duration in milliseconds
- status: `success` or `error`
- error string when failed
- selected metric samples when successful

The collector should parse Prometheus text exposition enough to identify metric names, labels, and float values. It does not need full Prometheus feature parity in this phase. Unknown or malformed lines should be counted as parse errors and skipped, not treated as fatal unless strict mode is enabled.

### Kernel Metrics

The collector emits aggregated kernel metrics:

- `scrape_success_total`
- `scrape_error_total`
- `scrape_parse_error_total`
- `scrape_latency`

These are collector health metrics, not WuKongIM business metrics.

WuKongIM metric samples go to `metrics.jsonl` and the compact summary output.

### Summary Output

The summary should include:

- scrape tick count
- per-node success count
- per-node error count
- total selected sample count
- scrape latency p95 and p99 in milliseconds
- latest selected gauges and counters for configured metric names

The summary must be bounded. If too many metric names match, keep the first configured maximum and record `dropped_metric_names`.

Default maximum:

```text
max_summary_metrics = 100
```

## Report Output

`report.json` should include:

- normal unit outputs and metrics
- background unit outputs and metrics
- artifact metadata
- unit timing fields

`summary.md` should include artifact links or relative paths. It should not inline raw metric samples.

Example Markdown entry:

```text
- `metrics` `wukongim.metrics_collector/v1` `completed`
  - output `summary` `port.wukongim.metrics_summary/v1`: scrapes `90`, errors `0`, samples `18450`
  - artifact `metrics.jsonl`: `artifacts/metrics/metrics.jsonl`, 2.4MB
  - metric `scrape_latency` `duration`: count `270`, avg `6.21ms`, p95 `10.00ms`, p99 `18.00ms`
```

## Example Scenario

```yaml
version: wkbench/v2

run:
  id: send-rate-with-metrics
  duration: 30s
  report_dir: ./reports/send-rate-with-metrics

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

  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: 1s
      timeout: 800ms
      path: /metrics
      include:
        - "wk_.*"
        - "wukongim_.*"
      fail_on_scrape_error: false

  identities:
    use: identity.pool
    after: [metrics]
    spec:
      total: 1000
      uid_prefix: metrics-u
      device_prefix: metrics-d
      token_prefix: metrics-token

  tokens:
    use: wukongim.prepare_tokens

  pairs:
    use: identity.person_pairs
    spec:
      count: 500
      mode: ring
      bidirectional: true

  sessions:
    use: wkproto.session_pool
    after: [tokens]
    spec:
      connect_rate: 100/s

  traffic:
    use: traffic.send
    after: [metrics]
    inputs:
      targets: pairs.targets
      sender: sessions.message_sender
    spec:
      rate: 1000/s
      payload_size: 128
      sender_pick: round_robin
      max_in_flight: 1000
      ack_timeout: 5s
```

The `metrics` unit starts after target readiness. The traffic path starts only after `metrics.Start` has returned. The metrics unit stops after `traffic` and any later foreground units finish.

## Send Rate Sweep Integration

Add script flags in the collector integration task:

```text
--collect-metrics
--metrics-interval D
--metrics-include REGEX_LIST
--metrics-exclude REGEX_LIST
```

When enabled, the sweep script renders `wukongim.metrics_collector` into each generated scenario and adds `after: [metrics]` to the first foreground workload setup unit. Mixed mode should render one collector per sub-scenario because group and person sub-scenarios are separate `wkbench run` processes.

## Testing Strategy

Kernel tests:

- A background unit starts before a dependent foreground unit and stops after it.
- A background unit's metrics emitted after foreground work are present in `UnitResult`.
- A background unit's reportable output is snapshotted after `Stop`.
- A background task fatal error cancels the foreground run.
- Foreground failure still stops active background tasks and records their partial results.
- Background tasks stop in reverse start order.
- Artifacts are created only for declared artifact names and appear in `UnitResult`.

Collector tests:

- Spec validation rejects zero interval, invalid regex, and invalid timeout.
- Scrape success writes JSONL samples and updates summary.
- Scrape errors are counted but do not fail by default.
- Strict scrape error mode reports a fatal background error.
- Include and exclude regexes filter metric names correctly.
- Malformed exposition lines increment parse error counts without failing in non-strict mode.

Script tests:

- `--collect-metrics` renders the metrics unit.
- Generated traffic/setup units depend on `metrics` so collection starts before workload.
- Dry-run remains jq-free.

## Rollout Plan

Implement this in three commits:

1. Add background lifecycle, timeline fields, and tests in the kernel.
2. Add artifact creation/reporting support and tests.
3. Add `wukongim.metrics_collector/v1`, example scenario, and sweep script flags.

This order keeps the framework behavior testable before adding WuKongIM-specific collection logic.
