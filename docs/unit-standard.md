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

## Create A Unit

Generate the standard package skeleton:

```bash
GOWORK=off go run ./cmd/wkbench new-unit \
  -kind demo.group_send_probe/v1 \
  -dir ./units/demo/group_send_probe \
  -title "Group send probe" \
  -description "Checks group SEND behavior through public wkbench ports."
```

The command creates:

```text
units/demo/group_send_probe/
  unit.go
  unit_test.go
  README.md
```

Run the generated unit tests first:

```bash
GOWORK=off go test ./units/demo/group_send_probe
```

The generated test uses `benchkit/unittest.AssertUnitContract`. Keep that test
and add focused tests for `Validate`, `Plan`, and `Run` as the unit gains
behavior.

## Implement Behavior

Unit code depends on contracts, ports, and direct external libraries only. It
does not import another unit package.

```go
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        "demo.group_send_probe/v1",
		Title:       "Group send probe",
		Description: "Checks group SEND behavior through public wkbench ports.",
		Inputs: []contract.PortDef{
			{Name: "summary", Type: trafficport.SummaryV1},
		},
	}
}

func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	summary, err := contract.Input[trafficport.Summary](env, "summary")
	if err != nil {
		return err
	}
	if summary.SendackErrors > 0 {
		return fmt.Errorf("sendack errors: %d", summary.SendackErrors)
	}
	return nil
}
```

If the unit produces a reportable output, return a small JSON-friendly value
that implements `contract.ReportableOutput`. Do not expose secrets, tokens, or
live client handles through report values.

## Register A Unit

Register units only in a distribution binary or test registry:

```go
func defaultRegistry() *registry.Registry {
	reg := registry.New()
	groupsendprobe.Register(reg)
	return reg
}
```

The unit package itself should not know which other units are present.

## Compose A Scenario

Composition belongs in YAML. Use explicit `inputs` when more than one provider
could satisfy the same port type.

```yaml
version: wkbench/v2

run:
  id: demo-probe
  duration: 5s
  report_dir: ./reports/demo-probe

units:
  groups:
    use: core.static_groups
    spec:
      count: 1
      members_per_channel: 2

  sender:
    use: core.fake_group_sender

  traffic:
    use: traffic.group_send
    spec:
      rate: 2/s
      payload_size: 128

  probe:
    use: demo.group_send_probe
    inputs:
      summary: traffic.summary
```

Run the scenario:

```bash
GOWORK=off go run ./cmd/wkbench run -scenario ./examples/group-send.yaml
```

When the scenario has `run.report_dir`, wkbench writes `report.json` and
`summary.md`. Outputs appear in the report only when the produced value opts in
with `contract.ReportableOutput`.

## Boundary Enforcement

The repository includes `units/import_boundary_test.go`. It scans production
unit code and fails if a unit imports another `units/*` package. Shared helpers
may live under a unit-domain `internal` package, such as
`units/wukongim/internal/benchapi`, because those helpers are not scenario
units and cannot be composed directly.
