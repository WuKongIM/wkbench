# wkbench Send Rate Sweep Design

## Goal

Add a repeatable way to find WuKongIM's highest sustainable send-link QPS with `wkbench`.

One measured operation remains `SEND -> SENDACK`. The sweep should discover the highest offered rate that satisfies the configured pass criteria for a chosen workload shape:

- person sends only
- group sends only
- mixed person and group sends

The output must make it easy to answer: "What is the highest passing QPS, which run failed first, and where are the detailed reports?"

## Non-Goals

- Do not implement a distributed coordinator in this phase.
- Do not change WuKongIM server behavior.
- Do not replace the existing single-run scenarios.
- Do not infer internal server bottlenecks automatically in this phase.
- Do not introduce a weighted global traffic scheduler in this phase; mixed load can still be represented by independent traffic units.

## Recommended Approach

Add a shell sweep runner in `wkbench/scripts`.

The first implementation should generate temporary `wkbench/v2` scenarios for each rate step and invoke the existing `wkbench run` command. This keeps the search logic outside the benchmark engine and reuses the existing unit graph, metrics, assertions, and report writer.

For `mixed` mode, the script must render separate person and group sub-scenarios for each step and run those two `wkbench run` processes concurrently. The `wkbench` kernel executes units in graph order, so putting two `traffic.send` units in a single scenario would measure sequential workloads rather than combined QPS.

Recommended script:

```text
scripts/bench-wukongim-three-node-send-rate-sweep.sh
```

The script should support three modes:

- `person`: only `person_traffic`
- `group`: only `group_traffic`
- `mixed`: both `person_traffic` and `group_traffic`

The default target is the local three-node WuKongIM v2 cluster started by `scripts/start-wukongimv2-three-nodes.sh`.

## Pass Criteria

Each step passes only when all configured report assertions pass.

Default criteria:

- `sendack_error_rate == 0`
- `wkbench run` exits successfully
- target readiness checks pass before the step starts

The design intentionally starts strict. Later work can add configurable thresholds such as `sendack_error_rate <= 0.001`, `p95 <= 200ms`, or `p99 <= 500ms`.

## Search Strategy

Use an explicit ordered rate list for the first implementation.

Example:

```bash
./scripts/bench-wukongim-three-node-send-rate-sweep.sh \
  --mode mixed \
  --rates 100,200,500,1000,2000,5000 \
  --duration 2m
```

The script runs rates in order and stops at the first failed step by default. The highest passing step is the answer for that run.

This explicit-list strategy is preferable for the first version because it is easy to reproduce, easy to inspect, and avoids hiding benchmark decisions behind a binary-search policy. A later version can add `--search binary` or `--ramp start,end,factor`.

## Workload Model

### Person Mode

`person` mode generates only a person send workload:

- `identity.pool`
- `wukongim.prepare_tokens`
- `identity.person_pairs`
- `wkproto.session_pool`
- `traffic.send` with person targets
- `report.assert`

The configured rate is assigned to `person_traffic.rate`.

### Group Mode

`group` mode generates only a group send workload:

- `identity.pool`
- `wukongim.prepare_tokens`
- `wukongim.prepare_group_channels`
- `wkproto.session_pool`
- `traffic.send` with group targets
- `report.assert`

The configured rate is assigned to `group_traffic.rate`.

### Mixed Mode

`mixed` mode generates both workloads as separate sub-scenarios that run at the same time.

The user provides either a person/group ratio or separate rates. The first implementation should support a ratio because it is the most useful way to ask "total QPS".

Default mixed ratio:

```text
person: 80%
group: 20%
```

For a total step rate of `1000/s`, the generated sub-scenarios use:

```text
person_rate = 800/s
group_rate = 200/s
```

The output should report both per-workload results and the total offered rate.

## Concurrency Model

The script must set `max_in_flight` high enough that the client does not cap the offered QPS before the target does.

Default formula:

```text
max_in_flight = max(1, ceil(rate * expected_latency_ms / 1000 * inflight_multiplier))
```

Default values:

- `expected_latency_ms`: `200`
- `inflight_multiplier`: `2`
- `max_in_flight_cap`: `20000`

For mixed mode, compute `max_in_flight` per workload from that workload's assigned rate.

The defaults are conservative enough for initial exploration. Users can override them when running higher-latency or higher-QPS tests.

## Script Options

Required or high-value options:

- `--mode person|group|mixed`
- `--rates LIST`, comma-separated total QPS steps
- `--duration D`, run duration per step
- `--users N`
- `--groups N`
- `--members N`
- `--person-pairs N`
- `--mixed-ratio PERSON:GROUP`, default `80:20`
- `--payload-size BYTES`, default `128`
- `--ack-timeout D`, default `5s`
- `--expected-latency-ms N`, default `200`
- `--inflight-multiplier N`, default `2`
- `--max-in-flight-cap N`, default `20000`
- `--out-dir DIR`, default `reports/send-rate-sweep/<timestamp>`
- `--start-target`, start the local three-node target before the sweep
- `--no-start-target`, require an already running target
- `--clean-target`, pass `--clean` to the target startup script
- `--keep-target`, leave the target running after the sweep

Default target endpoints:

```text
api_addrs: 5011, 5012, 5013
gateway_tcp_addrs: 5111, 5112, 5113
```

## Output Layout

For a sweep rooted at:

```text
reports/send-rate-sweep/20260602-153000
```

write:

```text
summary.md
summary.csv
steps/
  0001-100qps/
    scenario.yaml
    report.json
    summary.md
    console.txt
  0002-200qps/
    group/
      scenario.yaml
      report.json
      summary.md
      console.txt
    person/
      scenario.yaml
      report.json
      summary.md
      console.txt
```

Person-only and group-only steps use the flat `scenario.yaml` layout. Mixed steps use the `group/` and `person/` subdirectories so both workloads can run concurrently and keep separate reports.

`summary.md` should include:

- mode
- rates requested
- highest passing QPS
- first failing QPS, if any
- per-step status
- per-step sendack success count
- per-step sendack error count
- per-step sendack error rate
- per-step sendack latency avg/min/max in milliseconds
- mixed-mode aggregate total successes, errors, and error rate
- links or paths to step reports

`summary.csv` should include the same step-level machine-readable data.

## Data Extraction

The script should read each step's `report.json` with `jq` when available. If `jq` is missing, it should fail with a clear message instead of scraping Markdown.

For `person` and `group` modes, the script reads one traffic unit.

For `mixed` mode, the script reads `person_traffic` from the person sub-report and `group_traffic` from the group sub-report, then reports:

- per-workload values
- total sendack successes
- total sendack errors
- aggregate error rate

Latency is not aggregated across workloads in the first implementation. Mixed mode should show separate person and group latency values to avoid misleading averages; the aggregate row should mark latency as not applicable.

## Failure Handling

For each step:

- Validate the generated scenario before running.
- Capture console output to `console.txt`.
- Treat non-zero `wkbench run` exit as a failed step.
- Treat failed report assertions as a failed step.
- Keep the step report directory even on failure.
- Stop after the first failed step by default.

Target lifecycle:

- If the script starts the local three-node target, it should stop it on exit unless `--keep-target` is set.
- If `--no-start-target` is used, the script should not stop any existing target.

## Testing Strategy

Use TDD for the implementation.

Script tests:

- The sweep script passes `bash -n`.
- It requires `jq` or emits a clear error.
- It renders a person-mode scenario with only `person_traffic`.
- It renders a group-mode scenario with only `group_traffic`.
- It renders mixed-mode person and group sub-scenarios with the expected split rates.
- It does not render `0/s` workload rates for low mixed rates.
- It runs mixed-mode person and group sub-scenarios concurrently.
- It computes `max_in_flight` from rate, expected latency, multiplier, and cap.
- It writes deterministic step directories and summary paths.
- It uses `GOWORK=off go run ./cmd/wkbench validate` before each run.
- It captures `wkbench run` output into `console.txt`.

End-to-end smoke:

- Use `core.fake_message_sender/v1` only if a local no-target smoke path is needed.
- Real three-node target smoke can be manual or opt-in because it builds and starts WuKongIM.

## Rollout

1. Add the sweep script and script tests.
2. Add README usage examples.
3. Run script tests and full `GOWORK=off go test ./...`.
4. Run one short real three-node sweep with small rates, for example `10,20`, to prove the path works without turning the developer machine into a sustained load box.
