# wkbench Plugin Author Experience Design

## Goal

Make external wkbench plugins easy to create, verify, and hand off without
requiring plugin authors to patch or rebuild the wkbench repository.

The v1 experience should give a third-party author a clear local acceptance
loop:

```text
wkbench plugin init
go test ./...
go build -o ./bin/<plugin> ./cmd/<plugin>
wkbench plugin check ./bin/<plugin> -scenario ./examples/echo.yaml
wkbench plugin add <name> ./bin/<plugin>
wkbench validate -scenario ./examples/echo.yaml
```

The important outcome is confidence. A plugin author should know whether their
binary handshakes correctly, declares usable unit metadata, respects Phase 1
port boundaries, and can be referenced from scenario YAML before they give the
plugin to another wkbench user.

## Current State

The repository already has the core pieces:

- `wkbench plugin init` generates an external Go plugin module.
- `wkbench plugin add`, `list`, `doctor`, and `inspect` manage and inspect
  `.wkbench/plugins.yaml`.
- `-plugin <path>` loads an external plugin for `list-units`, `validate`,
  `explain`, `plan`, and `run`.
- Generated plugin projects currently include `go.mod`, a command package, one
  echo unit, one example scenario, and a README.
- `cmd/wkbench` tests already verify that a generated plugin project can run
  `go test ./...`, build a binary, and validate the generated scenario.

This means v1 does not need another plugin loader or another init command. The
gap is an explicit author-facing check command and stronger generated guidance.

## Non-Goals

- Do not build a plugin marketplace or remote installer.
- Do not solve plugin version pinning or binary distribution.
- Do not support non-Go SDK scaffolds in this phase.
- Do not expand the Phase 1 data transport contract.
- Do not make generated plugins import `units/*` from the wkbench repository.
- Do not make `plugin check` run live WuKongIM traffic or require a live target.

## Recommended Design

Add `wkbench plugin check` as the plugin author's local acceptance command.

The command checks one plugin binary or one configured plugin without requiring
the plugin to be permanently added to the project:

```bash
wkbench plugin check ./bin/acme-echo-plugin
wkbench plugin check acme.echo
wkbench plugin check ./bin/acme-echo-plugin -scenario ./examples/echo.yaml
```

`plugin check` starts the plugin with the same stdio RPC host path used by real
scenario commands, performs a handshake, validates the returned manifest, and
prints a compact report. With `-scenario`, it also loads only that plugin into a
registry and runs `validate`, `explain`, and `plan` against the scenario.

This keeps author validation separate from project configuration:

- `plugin inspect` shows what a plugin exposes.
- `plugin doctor` checks configured plugins in a wkbench project.
- `plugin check` answers whether a plugin binary is acceptable to distribute or
  use in a scenario.

## Command Behavior

Usage:

```text
wkbench plugin check <name-or-path> [-scenario path] [-timeout duration]
```

Target resolution follows `plugin inspect`:

- If `<name-or-path>` matches a configured plugin name, use that configured
  path relative to the project config directory.
- Otherwise treat the argument as an executable path.

The command should not load configured plugins unless the target name resolves
to one configured plugin. This avoids broken local project config blocking a
developer who only wants to check a newly generated binary.

The default timeout should match the existing manifest timeout. `-timeout`
allows slow debug builds to opt into a longer handshake window.

Exit codes:

- `0`: handshake, manifest validation, and optional scenario checks passed.
- `1`: plugin config, start, handshake, manifest, or scenario validation failed.
- `3`: internal CLI/reporting failure.

## Check Report

Text output should be readable in terminals and CI logs:

```text
Plugin check: ok
Plugin: acme.echo
Version: 0.1.0
Protocol: wkbench.plugin/v1
Source: ./bin/acme-echo-plugin

Units:
  - acme.echo/v1 ok
    outputs:
      result port.demo.echo/v1 data inline reportable

Scenario: ./examples/echo.yaml
  validate: ok
  explain: ok
  plan: ok
```

On failure, print the failing section and actionable reason:

```text
Plugin check: failed
Plugin: acme.echo

Units:
  - acme.secret/v1 failed
    output token_source: sensitive inline data ports cannot cross plugin RPC in Phase 1
```

JSON output can wait until there is a concrete consumer. v1 should keep the
surface small and human-focused.

## Manifest Validation

`plugin check` should validate the manifest returned by handshake before any
scenario work:

- Plugin name is non-empty.
- Plugin protocol is compatible with `wkbench.plugin/v1`.
- Each unit kind is non-empty and has a `/vN` version suffix.
- Unit kinds are unique within the plugin.
- Port names and types are non-empty.
- Any port that may cross plugin RPC must satisfy the current Phase 1 boundary
  rules:
  - `Boundary` is `data`.
  - `Transport` is `inline`.
  - `Sensitive` is false.
  - JSON is allowed when encodings are declared.
  - `MaxPayloadBytes`, when set, is positive.
- Artifacts have non-empty names.
- Background units are allowed, but their manifest flag should be displayed so
  users can tell which units run with `Start`/`Stop`.

The check should be stricter than `inspect` and friendlier than a runtime
failure. It should catch obvious authoring mistakes before a scenario reaches
the kernel.

## Scenario Validation Mode

When `-scenario` is provided, `plugin check` should create a temporary registry:

1. Register the host-local default units.
2. Load the target plugin only.
3. Run `kernel.Validate`.
4. Run `kernel.Explain`.
5. Run `kernel.Plan`.

The command should not load bundled official plugins by default in this mode.
Generated plugin scenarios should prove that the checked plugin is self-contained
where possible. If an author wants to validate against official plugins later,
that can be a separate option.

`plugin check -scenario` must not call `run` in v1. It should stay safe for
CI and author workstations, and it should not require live WuKongIM targets.

## Generated Plugin Template Changes

Enhance `wkbench plugin init` output without changing its required flags:

- Add `scripts/check.sh`.
- Expand README with:
  - build and test commands,
  - `wkbench plugin check` usage,
  - project registration with `plugin add`,
  - scenario usage with qualified unit kind,
  - Phase 1 port boundary limits,
  - release checklist for sharing the binary.
- Keep the generated echo unit as a compact example of:
  - JSON inline output,
  - reportable output,
  - host-managed artifact writing,
  - unit-local tests.

Generated `scripts/check.sh` should run:

```bash
go test ./...
go build -o ./bin/<command> ./cmd/<command>
wkbench plugin check ./bin/<command> -scenario ./examples/echo.yaml
```

The script should be POSIX shell compatible and not require `jq`.

## Documentation Updates

Update `docs/plugin-authoring.md` around the author journey:

1. Generate a plugin project.
2. Implement or edit units.
3. Run local unit tests.
4. Build the binary.
5. Run `wkbench plugin check`.
6. Add the plugin to a wkbench project.
7. Reference the qualified unit kind in scenario YAML.
8. Run `validate`, `explain`, `plan`, and eventually `run`.

The docs should make the split clear:

- `plugin check` is for plugin authors and CI.
- `plugin doctor` is for wkbench project users checking configured plugins.
- `plugin inspect` is for reading a manifest.

## Testing Strategy

Add focused tests in `cmd/wkbench`:

- `plugin check` succeeds for a generated plugin binary.
- `plugin check -scenario` succeeds for the generated plugin scenario.
- `plugin check` can resolve a configured plugin by name.
- `plugin check` ignores unrelated missing configured plugin paths when the
  target is a direct binary path.
- `plugin check` fails quickly for a hung plugin.
- `plugin check` reports invalid manifests, including duplicate unit kinds and
  missing version suffixes.
- `plugin init` generates `scripts/check.sh` and README content referencing
  `plugin check`.

Reuse existing test plugin builders where possible. Do not add network-dependent
tests.

## Acceptance Criteria

- A generated plugin project contains a working `scripts/check.sh`.
- The generated plugin can pass `go test ./...`, build, and pass
  `wkbench plugin check ./bin/<plugin> -scenario ./examples/echo.yaml`.
- `docs/plugin-authoring.md` documents the complete author flow.
- Existing `plugin add/list/doctor/inspect` behavior remains unchanged.
- `GOWORK=off go test ./cmd/wkbench ./benchkit/pluginhost ./sdk/go/wkbench/plugin -count=1`
  passes.
- `GOWORK=off go test ./...` passes before completion.

## Rollout

This is backward-compatible. Existing plugin binaries, project configs, and
scenario YAML continue to work.

After v1, the natural next phase is plugin distribution: local plugin cache,
version locks, install/update commands, and release metadata. That should build
on the check command rather than being mixed into it.
