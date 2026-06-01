# wkbench Plan Command Design

## Goal

Add a non-executing `wkbench plan` command that materializes each unit's deterministic `Plan` output before a benchmark run.

This makes the existing `contract.Unit.Plan` phase visible to users and creates the next bridge toward worker topology and distributed execution.

## Non-Goals

- Do not run units or touch target services.
- Do not write `plan.json` from `wkbench run` in this phase.
- Do not introduce distributed workers or worker topology configuration.
- Do not change the `contract.Unit` interface.
- Do not make the kernel understand unit-specific plan internals.
- Do not add raw metrics, artifacts, or execution outputs to plan results.

## Recommended Approach

Add planning as a first-class kernel operation:

```go
func (e *Engine) Plan(ctx context.Context, scenario dsl.Scenario) (PlanResult, error)
```

`Plan` should reuse the existing graph builder so it sees the same kind resolution, auto-wiring, and execution order as `validate`, `explain`, and `run`. For each unit, it calls `Validate`, then `Plan`, and records the returned `contract.Plan`. It must never call `Run`.

The CLI adds:

```bash
wkbench plan -scenario ./examples/group-send.yaml
wkbench plan -scenario ./examples/group-send.yaml -format json
```

Text output should be concise and deterministic. JSON output should marshal the full `kernel.PlanResult`.

## Result Model

Add these types to `benchkit/kernel`:

```go
type PlanResult struct {
	RunID  string                    `json:"run_id"`
	Status Status                    `json:"status"`
	Order  []string                  `json:"order"`
	Units  map[string]UnitPlanResult `json:"units"`
	Wiring []ExplainBinding          `json:"wiring,omitempty"`
}

type UnitPlanResult struct {
	Kind   string        `json:"kind"`
	Status Status        `json:"status"`
	Error  string        `json:"error,omitempty"`
	Plan   contract.Plan `json:"plan,omitempty"`
}
```

Use the existing `Status` enum:

- `completed`: every unit validated and planned.
- `config_failed`: graph building or validation failed.
- `plan_failed`: a unit `Plan` call failed.

`Wiring` should use the same binding shape as `Explain` so users can connect plan output back to graph input wiring without learning a second representation.

## Kernel Behavior

`Engine.Plan` should:

1. Initialize `PlanResult` with `RunID`, `StatusCompleted`, and an empty unit map.
2. Call `buildGraph`.
3. On graph error, set result status to `StatusConfigFailed` and return the error.
4. Copy `graph.order` into `PlanResult.Order`.
5. Populate `PlanResult.Wiring` from resolved bindings in execution order.
6. For each unit in order:
   - create `baseEnv` with the unit spec,
   - call `Validate`,
   - if validation fails, set overall status to `StatusConfigFailed`, record the unit error, and return,
   - call `Plan`,
   - if planning fails, set overall status to `StatusPlanFailed`, record the unit error, and return,
   - record `UnitPlanResult{Kind, StatusCompleted, Plan}`.

The method should not construct `runEnv`, output stores, metric stores, or cleanup hooks.

## CLI Behavior

The top-level usage should become:

```text
wkbench <list-units|new-unit|explain|plan|validate|run>
```

`runPlan` should follow the existing `runExplain` style:

- parse `-scenario`,
- parse `-format`, defaulting to `text`,
- support only `text` and `json`,
- return `exitConfig` for graph, validation, planning, parse, or unsupported format errors,
- return `exitInternal` only if JSON marshaling fails.

Text output should include:

```text
Run: group-send-demo

Execution Order:
  1. groups (core.static_groups/v1)
  2. sender (core.fake_group_sender/v1)
  3. traffic (traffic.group_send/v1)

Plans:
  groups: core.static_groups/v1
    status: completed
  traffic: traffic.group_send/v1
    status: completed
    shards: 1

Wiring:
  traffic.channels <- groups.groups (port.channel.group_set/v1)
```

For the first version, text rendering does not need to pretty-print arbitrary shard payloads. It should show a stable shard count when `Plan.Shards` is non-empty and rely on JSON output for full details.

## Unit Plan Detail

Most existing units currently return only `contract.Plan{UnitName: env.UnitName()}`. Keep that valid.

Update `traffic.group_send.Plan` to return one JSON-friendly shard describing deterministic work:

```go
type planShard struct {
	TotalMessages int64  `json:"total_messages"`
	RatePerSecond float64 `json:"rate_per_second"`
	DurationMS    int64  `json:"duration_ms"`
	PayloadSize   int    `json:"payload_size"`
	SenderPick    string `json:"sender_pick,omitempty"`
	MaxInFlight   int    `json:"max_in_flight,omitempty"`
}
```

Compute `TotalMessages` the same way `Run` does:

```go
int64(math.Round(spec.Rate.PerSecond * env.RunDuration().Seconds()))
```

Clamp it to at least one message, matching current runtime behavior.

This gives `wkbench plan` immediate value on the dry group-send example without making the kernel aware of traffic internals.

## Error Handling

- Graph and validation failures are configuration failures.
- Unit planning failures are plan failures.
- The returned `PlanResult` should include any unit results recorded before the failure plus the failed unit result.
- `wkbench plan` should not write reports and should not create `run.report_dir`.
- `Plan` errors should use the same wrapping style as `Run`, for example `unit "traffic" plan: ...`.

## Tests

Kernel tests:

- `Engine.Plan` returns run id, status, execution order, unit kind, unit plan, and wiring.
- `Engine.Plan` calls `Validate` and `Plan`, but not `Run`.
- `Engine.Plan` returns `StatusPlanFailed` and the failed unit result when a unit plan fails.

CLI tests:

- `wkbench plan -scenario scenario.yaml` prints `Execution Order`, `Plans`, and a wiring line.
- `wkbench plan -scenario scenario.yaml -format json` unmarshals into `kernel.PlanResult` and contains expected order and plan data.
- Unsupported `-format` returns `exitConfig`.

Unit tests:

- `traffic.group_send.Plan` reports deterministic `total_messages`, `rate_per_second`, `duration_ms`, and `payload_size`.
- The existing run behavior remains unchanged.

Smoke:

```bash
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml -format json
```

Both commands should exit 0. The JSON smoke should include `total_messages` for `traffic`.

## Rollout

Implement as a focused feature branch:

1. Add kernel plan result types and `Engine.Plan` with tests.
2. Add CLI `plan` text and JSON rendering with tests.
3. Add useful `traffic.group_send.Plan` shard details with tests.
4. Update README and scenario DSL docs with `wkbench plan` usage.
5. Run full tests and CLI smoke.

