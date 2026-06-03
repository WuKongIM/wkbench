# wkbench Phase 2B Plugin UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make external wkbench plugins easy to configure, inspect, diagnose, and scaffold without changing the RPC protocol.

**Architecture:** Keep plugin UX in `cmd/wkbench` as a thin CLI/config layer. Runtime commands merge project `.wkbench/plugins.yaml` with repeated global `-plugin` flags, while management commands can read or repair config without starting every configured plugin.

**Tech Stack:** Go `flag`, `gopkg.in/yaml.v3`, existing `pluginhost` stdio client, existing Go SDK plugin server, CLI integration tests.

---

### Task 1: Project Plugin Config

**Files:**
- Create: `cmd/wkbench/plugin_config.go`
- Modify: `cmd/wkbench/main.go`
- Test: `cmd/wkbench/main_test.go`

- [x] Add `.wkbench/plugins.yaml` discovery by walking upward from the current directory.
- [x] Support `version: wkbench.plugins/v1` and `plugins: [{name, path, enabled}]`.
- [x] Resolve relative paths from the project directory containing `.wkbench`.
- [x] Merge configured plugin paths with global `-plugin` paths and dedupe by canonical path.
- [x] Verify `validate` and `list-units` auto-load configured plugins.

### Task 2: Plugin Management Commands

**Files:**
- Create: `cmd/wkbench/plugin_commands.go`
- Modify: `cmd/wkbench/main.go`
- Test: `cmd/wkbench/main_test.go`

- [x] Add `wkbench plugin list`.
- [x] Add `wkbench plugin add <name> <path>`.
- [x] Add `wkbench plugin doctor`.
- [x] Add `wkbench plugin inspect <name-or-path>`.
- [x] Ensure `plugin add`, `plugin init`, and `plugin list` do not start configured plugins.

### Task 3: External Plugin Template

**Files:**
- Create: `cmd/wkbench/plugin_init.go`
- Test: `cmd/wkbench/main_test.go`

- [x] Add `wkbench plugin init -dir <dir> -module <module> -name <plugin-name>`.
- [x] Generate a standalone Go module with a minimal echo unit, command entrypoint, scenario, README, `.gitignore`, `go.mod`, and `go.sum`.
- [x] Verify generated plugin `go test ./...`, `go build`, and host scenario validation.

### Task 4: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/plugin-authoring.md`
- Create: `docs/superpowers/plans/2026-06-03-wkbench-phase-2b-plugin-ux.md`

- [x] Document the recommended `plugin init -> build -> plugin add -> plugin doctor -> run` workflow.
- [x] Document `.wkbench/plugins.yaml`.
- [x] Document management command behavior and Phase 1 limits.

### Verification

- [x] Focused CLI plugin UX tests.
- [x] `GOWORK=off go test ./...`
- [x] Demo plugin build/validate/run smoke.
- [x] Official data plugin build/validate/run smoke.
