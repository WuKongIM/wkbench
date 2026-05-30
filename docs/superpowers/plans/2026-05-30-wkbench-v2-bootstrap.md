# wkbench v2 Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap the independent `WuKongIM/wkbench` repository with a v2 unit architecture, public ports, a graph kernel, sample units, CLI, docs, and tests.

**Architecture:** The kernel validates and runs a scenario DAG. Units never depend on other units; they only consume and provide versioned ports. The CLI builds the default distribution by registering built-in units.

**Tech Stack:** Go 1.23, stdlib, `gopkg.in/yaml.v3`, Go unit tests.

---

## File Structure

- `benchkit/contract`: Unit interfaces, definitions, typed rate/duration helpers, env contracts.
- `benchkit/ports/channel`: `GroupSet` output port for group channel providers.
- `benchkit/ports/wkproto`: `GroupSender` input port for group traffic.
- `benchkit/ports/traffic`: `Summary` output port for traffic units.
- `benchkit/registry`: Unit registration and default-version kind resolution.
- `benchkit/dsl`: YAML scenario parsing and variable expansion.
- `benchkit/kernel`: Scenario validation, auto-wiring, topological execution, metrics and outputs.
- `benchkit/report`: JSON and Markdown report writer.
- `units/core/static_groups`: Demo group-set provider.
- `units/core/fake_group_sender`: Demo group sender provider for runnable examples.
- `units/traffic/group_send`: Group SEND -> SENDACK unit using only public ports.
- `units/report/assert`: Assertion unit over traffic summaries.
- `cmd/wkbench`: CLI for `list-units`, `validate`, and `run`.

## Tasks

- [x] Add failing tests for registry default-version resolution.
- [x] Implement `contract` and `registry` until registry tests pass.
- [x] Add failing tests for DSL variable expansion.
- [x] Implement `dsl` until parser tests pass.
- [x] Add failing tests for kernel auto-wiring and ambiguity detection.
- [x] Implement `kernel` graph validation and sequential run until tests pass.
- [x] Add failing tests for `traffic.group_send` using fake public ports.
- [x] Implement channel, wkproto, traffic ports and `traffic.group_send`.
- [x] Add core demo units and report assertion unit.
- [x] Add CLI and report writer tests.
- [x] Run `go test ./...`.
- [x] Update README and examples with the runnable scenario.
