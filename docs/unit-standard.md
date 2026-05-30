# Unit Authoring Standard

## Dependency Rules

A unit may depend on:

- `benchkit/contract`
- `benchkit/ports/*`
- external libraries it directly needs, such as HTTP or protocol codecs

A unit must not import another `units/*` package.

## Required Interface

Every unit implements:

```go
type Unit interface {
    Definition() Definition
    Validate(context.Context, ValidateEnv) error
    Plan(context.Context, PlanEnv) (Plan, error)
    Run(context.Context, RunEnv) error
}
```

## Method Responsibilities

- `Definition`: declare kind, input ports, output ports, metrics, and artifacts.
- `Validate`: decode and validate local spec only. Do not open sockets or call target APIs.
- `Plan`: compute deterministic work from spec, run settings, workers, and seed.
- `Run`: read inputs through `RunEnv.Input`, set outputs through `RunEnv.SetOutput`, and emit metrics/events through the env.

## Ports

Ports describe capabilities, not implementations. They live under `benchkit/ports`.

Good:

```text
port.channel.group_set/v1
port.wkproto.group_sender/v1
port.traffic.summary/v1
```

Avoid large all-purpose ports. Prefer small capability interfaces such as group sending, person sending, receive acknowledgments, and session snapshots.

## Package Layout

```text
units/traffic/group_send/
  unit.go
  unit_test.go
  README.md
```

Larger units may split `spec.go`, `plan.go`, `run.go`, and `metrics.go`, but only when the split keeps files focused.

## Test Expectations

- Validate tests cover legal and illegal specs.
- Plan tests prove deterministic output.
- Run tests use fake public ports instead of real target services.
- Metric tests verify important counters and summaries.
- Kernel fixture tests verify graph wiring for expected scenario usage.

Use `contract.NewTestRunEnv` for focused unit tests.
