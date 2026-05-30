# WuKongIM Black-Box Units Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add real WuKongIM black-box units that can prepare benchmark data through `/bench/v1/*`, open WKProto sessions, and feed the existing `traffic.group_send` unit without introducing unit-to-unit code dependencies.

**Architecture:** Add versioned public ports for targets and identities. Implement `wukongim.target`, `identity.pool`, `wukongim.prepare_tokens`, `wukongim.prepare_group_channels`, and `wkproto.session_pool` as independent units that depend only on `benchkit/contract`, public ports, and direct external libraries.

**Tech Stack:** Go 1.23, stdlib HTTP/TCP, `github.com/WuKongIM/WuKongIM/pkg/protocol/*`, `gopkg.in/yaml.v3`.

---

## Tasks

- [x] Add failing tests for target and identity public ports.
- [x] Implement `ports/target`, `ports/identity`, and `identity.pool/v1`.
- [x] Add failing tests for `wukongim.target/v1` and bench API HTTP client requests.
- [x] Implement `wukongim.target/v1` and a small black-box bench API client.
- [x] Add failing tests for `wukongim.prepare_tokens/v1`.
- [x] Implement token preparation and token-source output.
- [x] Add failing tests for `wukongim.prepare_group_channels/v1`.
- [x] Implement group channel preparation and `port.channel.group_set/v1` output.
- [x] Add failing tests for `wkproto.session_pool/v1` using fake public inputs and a fake WKProto-capable client factory.
- [x] Implement session pool unit, production WKProto client adapter, and CLI registration.
- [x] Add `examples/wukongim-group-send.yaml` and update docs.
- [x] Run `GOWORK=off go test ./...`.
