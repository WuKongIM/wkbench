# wkbench Phase 2C Official Plugins Default Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make wkbench load safe official units through bundled plugin executables by default while keeping capability/background units local until capability and background RPC exist.

**Architecture:** Add official plugin packages and command entrypoints for units whose ports are inline JSON data across the plugin boundary. Add a default official plugin load path in `cmd/wkbench`; runtime commands start bundled official plugins through a hidden self-sidecar command plus project/CLI plugins, while `plugin` and `new-unit` remain management/scaffold commands that do not start plugins. The host registry is reduced to local capability, token-source, and background lifecycle units only.

**Tech Stack:** Go, existing stdio plugin protocol, existing `sdk/go/wkbench/plugin`, existing CLI flag parser, `os.Executable`, `go test`, scenario YAML.

---

### Task 1: Official Plugin Packages

**Files:**
- Create: `plugins/official/core/plugin.go`
- Create: `plugins/official/core/cmd/wkbench-official-core-plugin/main.go`
- Create: `plugins/official/identity/plugin.go`
- Create: `plugins/official/identity/cmd/wkbench-official-identity-plugin/main.go`
- Create: `plugins/official/wukongim/plugin.go`
- Create: `plugins/official/wukongim/cmd/wkbench-official-wukongim-plugin/main.go`
- Create: `plugins/official/report/plugin.go`
- Create: `plugins/official/report/cmd/wkbench-official-report-plugin/main.go`
- Modify: `plugins/official/dataplane/plugin.go`
- Test: `plugins/official/*`

- [ ] Write plugin package tests that assert manifests contain expected unit kinds.
- [ ] Move pure data/report official units into grouped plugin manifests:
  - `wkbench.official.core`: `core.static_groups/v1`
  - `wkbench.official.identity`: `identity.pool/v1`, `identity.person_pairs/v1`
  - `wkbench.official.wukongim`: `wukongim.target/v1`, `wukongim.prepare_group_channels/v1`
  - `wkbench.official.report`: `report.assert/v1`
- [ ] Keep `plugins/official/dataplane` as a compatibility plugin that re-exports the data/report subset for the existing example.

### Task 2: Default Official Plugin Loading

**Files:**
- Create: `cmd/wkbench/official_plugins.go`
- Modify: `cmd/wkbench/main.go`
- Test: `cmd/wkbench/main_test.go`

- [ ] Write failing CLI tests proving `list-units` still includes official units after removing direct data/report registrations.
- [ ] Write failing tests proving default official plugin paths are loaded before project plugins and can be disabled with a global flag.
- [ ] Add a global `-no-official-plugins` flag for tests and escape hatches.
- [ ] Implement bundled official plugin startup through `os.Executable()` and a hidden `__official-plugin <name>` sidecar command so `go run ./cmd/wkbench ...` works without prebuilt sidecar files.
- [ ] Add test override hooks so `cmd/wkbench` tests can point default official plugins at temporary binaries.
- [ ] Reduce `defaultRegistry` to local capability units only:
  - `core.fake_group_sender/v1`
  - `core.fake_message_sender/v1`
  - `wkproto.session_pool/v1`
  - `wukongim.prepare_tokens/v1`
  - `wukongim.metrics_collector/v1`
  - `traffic.group_send/v1`
  - `traffic.send/v1`

### Task 3: Scenario Compatibility

**Files:**
- Modify: `cmd/wkbench/main_test.go`
- Modify: `examples/official-data-plugin.yaml`
- Test: `cmd/wkbench/main_test.go`

- [ ] Add tests that build official plugin binaries and validate/run `examples/group-send.yaml` using default official plugins.
- [ ] Add tests that validate/explain/plan WuKongIM scenarios with official data/wukongim/report plugins and local capability units.
- [ ] Keep existing explicit `wkbench.official.data:*` example working.

### Task 4: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/plugin-authoring.md`
- Modify: `docs/design/wkbench-v2-unit-architecture.md`
- Create: `docs/superpowers/plans/2026-06-03-wkbench-phase-2c-official-plugins-default-runtime.md`

- [ ] Document official plugins loaded by default.
- [ ] Document `-no-official-plugins`.
- [ ] Document why capability units stay local until capability RPC exists and background units stay local until background lifecycle RPC exists.

### Verification

- [ ] Focused tests:
  `GOWORK=off go test ./cmd/wkbench ./plugins/official/core ./plugins/official/identity ./plugins/official/wukongim ./plugins/official/report ./plugins/official/dataplane -count=1`
- [ ] Full tests:
  `GOWORK=off go test ./...`
- [ ] Build official plugin binaries.
- [ ] Validate and run `examples/group-send.yaml` with default official plugins.
- [ ] Validate/explain/plan WuKongIM example scenarios.
