# Scenario DSL

The v2 scenario DSL describes a graph of units.

```yaml
version: wkbench/v2
run:
  id: demo
  duration: 2s
  report_dir: ./reports/demo

units:
  groups:
    use: core.static_groups
    spec:
      count: 2
      members_per_channel: 3

  sender:
    use: core.fake_group_sender

  traffic:
    use: traffic.group_send
    spec:
      rate: 5/s
```

## `use`

`use` selects a registered unit. If the version is omitted, the registry resolves it only when exactly one matching version exists.

## `inputs`

Inputs wire a unit-local input port to an upstream output:

```yaml
limits:
  use: report.assert
  inputs:
    summary: traffic.summary
```

Inputs may be omitted when the kernel can find exactly one provider with the required port type. Ambiguous matches are rejected.

Inspect resolved wiring before a run:

```bash
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-group-send.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-group-send.yaml -format json
```

`explain` uses the same graph builder as `validate` and `run`, so auto-wired inputs and execution order match runtime behavior. It calls unit validation only; it does not plan, run, write reports, or contact target services.

Materialize deterministic unit plans before a run:

```bash
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-group-send.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-group-send.yaml -format json
```

`plan` uses the same graph builder as `explain`, then calls each unit's validation and `Plan` phase in execution order. It shows per-unit plan status and shard counts in text mode; JSON mode contains the full unit-owned `contract.Plan`, such as `traffic.group_send/v1` total message count and rate. It does not run units, publish outputs, write reports, or contact target services.

## `after`

Use `after` for ordering dependencies that do not pass data.

```yaml
traffic:
  use: traffic.group_send
  after: [readyz]
```

## `vars`

Variables are simple whole-value substitutions:

```yaml
vars:
  qps: 500/s

units:
  traffic:
    use: traffic.group_send
    spec:
      rate: ${qps}
```

The DSL intentionally does not support expressions, loops, or conditionals. Complex behavior should be modeled as units.
