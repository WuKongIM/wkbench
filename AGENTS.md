# AGENTS.md

Agent instructions for `wkbench`.

## Project Rules

- `wkbench` is a Go benchmark toolkit. The kernel runs scenario graphs; units
  expose capabilities through versioned ports.
- Composition belongs in scenario YAML, not in unit-to-unit imports.
- Do not import another `units/*` package from a unit.
- Ports live in `benchkit/ports/*`; runtime contracts live in
  `benchkit/contract`.
- CLI unit registration belongs in `cmd/wkbench`.

## Commands

Run commands from the `wkbench` root and use `GOWORK=off`.

```bash
GOWORK=off go test ./...
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench run -scenario ./examples/group-send.yaml
```

Use focused tests while editing; run `GOWORK=off go test ./...` before claiming
completion.

## Unit Standards

- `Definition` declares kind, ports, metrics, and artifacts.
- `Validate` checks local spec only. It must not call WuKongIM or open sockets.
- `Plan` must be deterministic.
- `Run`/`Start` may read inputs, emit metrics, set outputs, and write artifacts.
- Keep report outputs JSON-friendly. Do not expose tokens, clients, file handles,
  or secrets.
- Kernel duration metrics are stored in seconds; user-facing latency reports use
  milliseconds where existing reports do so.
- Raw large samples belong in artifacts, not inline in `summary.md`.

## Tests

Place tests at the layer that owns the behavior:

- contracts: `benchkit/contract`, `benchkit/unittest`
- graph/runtime: `benchkit/kernel`
- report rendering: `benchkit/report`
- CLI registration/scenario validation: `cmd/wkbench`
- unit behavior: the unit package under `units/*`
- script rendering: `scripts`

Network-dependent behavior should be covered by explicit smoke scripts or tests
with local test servers.

## Scripts And Scenarios

- Use explicit YAML `inputs` when more than one unit can provide a port.
- Use `after` for ordering requirements, especially background collectors.
- Dry-run scripts must not require `jq` or a live WuKongIM target.
- Do not start, clean, or stop local WuKongIM targets unless the user or script
  option explicitly requests it.


## Done Criteria

Before reporting completion:

1. Run focused tests for changed areas.
2. Run `GOWORK=off go test ./...`.
3. Run relevant scenario/script validation.
4. Confirm `git status --short` shows only intentional changes.
