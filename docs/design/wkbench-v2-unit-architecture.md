# wkbench v2 Unit Architecture

## Goal

`wkbench` v2 is an independent benchmark repository built around composable units. The benchmark kernel does not know WuKongIM business behavior. It loads a scenario graph, validates port wiring, plans deterministic work, runs units, collects metrics and artifacts, and writes reports.

## Core Principles

- Units never import other units.
- Units communicate only through versioned public ports under `benchkit/ports/*`.
- A unit can depend on `benchkit/contract`, relevant port packages, and the external libraries it directly needs.
- Scenario YAML is the only place where units are composed.
- `Validate` does not perform network IO.
- `Plan` is deterministic for the same scenario, seed, and worker topology.
- `Run` reads inputs, emits metrics/events/artifacts, and sets outputs only through `RunEnv`.
- Reports are written by the kernel/report package, not by individual traffic units.

## Repository Layout

```text
cmd/wkbench/              thin CLI entry point
benchkit/contract/        stable unit API, shared value types, run env interfaces
benchkit/dsl/             scenario YAML parsing and variable expansion
benchkit/kernel/          graph validation, planning, execution, result model
benchkit/ports/           versioned capability ports shared by units
benchkit/registry/        unit registry and kind resolution
benchkit/report/          report directory writer
plugins/official/         bundled official plugin executables
plugins/demo/             demo external plugin
units/core/               generic demo/core units
units/traffic/            traffic generators
units/report/             assertion and report-helper units
examples/                 runnable scenario examples
docs/                     design and authoring documentation
```

## Dependency Direction

```text
cmd/wkbench
  -> benchkit/kernel
  -> benchkit/pluginhost
  -> bundled official plugin packages for sidecar serving
  -> host-local runtime unit packages for registration

benchkit/kernel
  -> benchkit/contract
  -> benchkit/dsl
  -> benchkit/registry

units/*
  -> benchkit/contract
  -> benchkit/ports/*

benchkit/contract
  -> Go stdlib only
```

`benchkit/kernel` must not import `units/*`. The CLI assembles a distribution by registering the units it wants to ship.

The default distribution registers host-local runtime units that still require
Go capability ports, local resources, token-source interfaces, or background
lifecycles, and loads bundled official plugins for data and control-plane
units. Those kind sets are intentionally disjoint. Scenario composition does
not change: YAML refers to unit kinds and port names, while the host decides
whether the implementation is remote or local.

## Scenario DSL

The DSL is intentionally small. It is a unit graph with optional variable substitution.

```yaml
version: wkbench/v2
run:
  id: group-send-demo
  duration: 2s
  report_dir: ./reports/group-send-demo

vars:
  groups: 2
  members: 3
  rate: 5/s

units:
  groups:
    use: core.static_groups
    spec:
      count: ${groups}
      members_per_channel: ${members}

  sender:
    use: core.fake_group_sender

  traffic:
    use: traffic.group_send
    spec:
      rate: ${rate}
      payload_size: 64

  limits:
    use: report.assert
    inputs:
      summary: traffic.summary
    spec:
      rules:
        - metric: sendack_error_rate
          op: eq
          value: 0
```

Inputs may be omitted when the kernel can find exactly one output in the graph with the required port type. If there are zero or multiple candidates, the scenario must wire the input explicitly.

## Unit Standard

Each unit implements:

```go
type Unit interface {
    Definition() Definition
    Validate(context.Context, ValidateEnv) error
    Plan(context.Context, PlanEnv) (Plan, error)
    Run(context.Context, RunEnv) error
}
```

`Definition` declares input ports, output ports, metrics, and artifacts. Port names belong to the unit. Port types belong to public port packages and are versioned strings such as `port.channel.group_set/v1`.

## Report Model

The kernel records per-unit status, metrics, events, artifacts, outputs, and errors. The report writer creates a deterministic report directory with at least:

```text
report.json
summary.md
```

Later phases can add `graph.json`, `plan.json`, raw metrics, and per-unit artifact files without changing unit contracts.
