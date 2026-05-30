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
