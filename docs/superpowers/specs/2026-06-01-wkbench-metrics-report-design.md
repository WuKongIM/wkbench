# wkbench Metrics Report Design

## Goal

Add a compact metrics result chain for `wkbench` so metrics emitted by units are visible in `report.json` and `summary.md`.

This closes the current gap between the unit contract and the report output: units can already call `EmitCounter` and `ObserveDuration`, and definitions can already declare metrics, but the kernel does not yet preserve those measurements in the scenario result.

## Non-Goals

- Do not change the unit contract method signatures.
- Do not store raw metric samples.
- Do not add percentiles, histograms, or buckets in this phase.
- Do not add Prometheus, OpenTelemetry, or an external metrics backend.
- Do not design distributed worker metric merging in this phase.

## Recommended Approach

Use process-local aggregation inside `benchkit/kernel`.

Each unit receives its own `runEnv`. That environment aggregates metrics emitted during `Run`. When the unit finishes, the kernel copies the aggregate metrics into `kernel.UnitResult`. The existing `benchkit/report` package renders the metrics from `kernel.Result`.

This keeps responsibilities narrow:

- Units emit metrics through the stable `RunEnv` API.
- The kernel owns execution-time aggregation and result shape.
- The report package owns JSON and Markdown rendering.
- Port packages remain focused on data capabilities, not metric storage.

## Result Model

Add metrics to `kernel.UnitResult`:

```go
type UnitResult struct {
    Kind    string                  `json:"kind"`
    Status  Status                  `json:"status"`
    Error   string                  `json:"error,omitempty"`
    Outputs map[string]OutputResult `json:"outputs,omitempty"`
    Metrics map[string]MetricResult `json:"metrics,omitempty"`
    Cleanup []CleanupResult         `json:"cleanup,omitempty"`
}

type MetricResult struct {
    Type   string          `json:"type"`
    Labels contract.Labels `json:"labels,omitempty"`
    Count  int64           `json:"count"`
    Sum    float64         `json:"sum"`
    Min    float64         `json:"min,omitempty"`
    Max    float64         `json:"max,omitempty"`
}
```

Metric keys should be deterministic. The first phase can key unlabelled metrics by metric name. If labels are present, use a stable encoded key derived from metric name plus sorted labels, while keeping labels structured in `MetricResult`.

## Aggregation Semantics

Counters:

- `EmitCounter(name, delta, labels)` increments `Count` by one.
- `Sum` is the accumulated delta.
- `Type` is `counter`.
- `Min` and `Max` can remain omitted for counters.

Durations:

- `ObserveDuration(name, value, labels)` increments `Count` by one.
- `Sum`, `Min`, and `Max` are recorded in seconds.
- Average is derived by the report layer as `Sum / Count`.
- `Type` is `duration`.

The kernel should preserve metrics emitted before a unit fails. That means failed units can still show counters and durations that help explain the failure.

## Metric Definition Handling

Unit definitions already declare `Metrics []contract.MetricDef`. The kernel should use those definitions to set known metric types when rendering results.

If a unit emits a metric that was not declared, the first version should still record it. Undeclared counters use type `counter`; undeclared duration observations use type `duration`. This keeps development flexible while reports remain useful.

## Report Output

`report.json` should include the structured `metrics` map under each unit result.

`summary.md` should render metrics under each unit after outputs and before cleanup entries:

```text
- `traffic` `traffic.group_send/v1` `completed`
  - metric `send_attempt_total` `counter`: count `10`, sum `10`
  - metric `sendack_latency` `duration`: count `10`, avg `0.0012s`, min `0.0008s`, max `0.0021s`
```

Counters should show count and sum. Durations should show count, avg, min, and max in seconds with compact formatting.

## Data Flow

```text
unit.Run
  -> env.EmitCounter / env.ObserveDuration
  -> runEnv metric accumulator
  -> kernel.UnitResult.Metrics
  -> benchkit/report report.json
  -> benchkit/report summary.md
```

No unit should write report files directly, and no report code should call unit code.

## Error Handling

- Invalid metric names are not introduced as a new error condition in this phase.
- Metrics emitted before a unit run error are preserved on that failed unit result.
- Metric aggregation failures should not exist for normal in-memory aggregation. If JSON rendering fails, `report.WriteDir` should return the existing report write error path.
- Cleanup behavior remains unchanged.

## Testing Strategy

Kernel tests:

- A test unit emits counters and durations; `Run` returns `UnitResult.Metrics`.
- Counter aggregation verifies count and sum.
- Duration aggregation verifies count, sum, min, and max in seconds.
- A failing unit emits a metric before returning an error; the failed `UnitResult` still contains that metric.

Report tests:

- `report.json` includes metrics through the existing result marshal path.
- `summary.md` renders counter metrics.
- `summary.md` renders duration metrics with avg, min, and max.

Scenario smoke:

- Run `examples/group-send.yaml`.
- Verify the generated report includes `send_attempt_total`, `sendack_success_total`, `sendack_error_total`, and `sendack_latency`.

## Rollout

Implement as a single focused feature branch:

1. Add kernel metric result types and aggregation tests.
2. Wire `runEnv.EmitCounter` and `runEnv.ObserveDuration` into the accumulator.
3. Add report rendering tests and Markdown output.
4. Run full tests and a dry scenario report smoke.
5. Update `docs/unit-standard.md` with the metric reporting behavior.

