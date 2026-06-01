# wkbench Send Rate Workload Design

## Goal

Add a composable `wkbench` send-rate workload for WuKongIM where one measured operation is `SEND -> SENDACK`.

The design must support multiple workload shapes in one scenario, such as group sends and person sends running at different rates. Each workload reports its own send attempts, sendack success count, sendack errors, and sendack round-trip latency.

## Non-Goals

- Do not measure end-to-end delivery, `Recv`, or `RecvAck` in this phase.
- Do not add distributed worker coordination in this phase.
- Do not add a global mixed-workload scheduler with weighted traffic in this phase.
- Do not remove existing `traffic.group_send/v1` scenario compatibility.
- Do not change WuKongIM server behavior.

## Recommended Approach

Use independent traffic units for independent workload rates.

A scenario should express mixed load by composing multiple units:

```yaml
group_traffic:
  use: traffic.send
  inputs:
    targets: groups.targets
    sender: sessions.message_sender
  spec:
    rate: 500/s
    payload_size: 128

person_traffic:
  use: traffic.send
  inputs:
    targets: pairs.targets
    sender: sessions.message_sender
  spec:
    rate: 2000/s
    payload_size: 128
```

This keeps each rate, target set, and result independent. A later weighted mixer can be added on top if users need "one total rate split by weights" semantics.

## Public Ports

Add a generic send target port:

```text
port.channel.send_target_set/v1
```

It exposes deterministic send targets:

```go
type SendTargetSet interface {
    Count() int
    At(index int) SendTarget
}

type SendTarget struct {
    ChannelID   string   `json:"channel_id"`
    ChannelType uint8    `json:"channel_type"`
    SenderUIDs  []string `json:"sender_uids"`
}
```

For group sends, `ChannelType` is `2` and `SenderUIDs` are the prepared group members. For person sends, `ChannelType` is `1`; `ChannelID` is the recipient uid from the sender's protocol perspective, and `SenderUIDs` contains the sender uid or allowed sender uids for that pair.

Add a generic WKProto sender port:

```text
port.wkproto.message_sender/v1
```

It sends one message to a requested channel type and waits for the matching sendack:

```go
type MessageSender interface {
    MessageClient(uid string) (MessageClient, bool)
}

type MessageClient interface {
    SendAndWaitAck(context.Context, SendRequest) (SendAck, error)
}

type SendRequest struct {
    ChannelID   string
    ChannelType uint8
    SenderUID   string
    ClientMsgNo string
    Payload     []byte
    Timeout     time.Duration
}
```

Keep the existing group sender port for compatibility. `wkproto.session_pool/v1` should output both the old `group_sender` and the new `message_sender` during the migration.

## Units

### `traffic.send/v1`

`traffic.send/v1` is the primary measured workload.

Inputs:

- `targets`: `port.channel.send_target_set/v1`
- `sender`: `port.wkproto.message_sender/v1`

Output:

- `summary`: `port.traffic.summary/v1`

Spec:

- `rate`: total offered send rate for this workload.
- `payload_size`: deterministic payload size.
- `sender_pick`: `first_online` or `round_robin`.
- `ack_timeout`: per-message sendack timeout, defaulting to the existing traffic timeout.
- `max_in_flight`: maximum concurrent `SEND -> SENDACK` operations. `0` or `1` uses serial execution; values greater than `1` enable bounded concurrency.

Runtime behavior:

- Compute total messages from `rate * run.duration`, rounded with the existing `totalMessages` behavior.
- Pace send starts over `run.duration` according to `rate`; a workload that falls behind schedule sends the next eligible operation immediately while respecting `max_in_flight`.
- Pick targets round-robin across the target set.
- Pick a sender from the target's `SenderUIDs` according to `sender_pick`.
- Send with deterministic `ClientMsgNo` and payload.
- Measure successful sendack latency from immediately before protocol send until the matching sendack returns.
- Record success and error counters, then publish `traffic.Summary`.

### Group Target Production

`wukongim.prepare_group_channels/v1` should continue to output the existing group channel set. It should also output `targets` as `port.channel.send_target_set/v1` so the new generic send workload can use prepared group channels.

The existing `traffic.group_send/v1` can remain for old scenarios. It may later become a compatibility wrapper around the generic send workload, but that is not required for the first implementation.

### Person Target Production

Add `identity.person_pairs/v1` to generate deterministic person send targets from an identity pool.

Inputs:

- `identities`: `port.identity.pool/v1`

Output:

- `targets`: `port.channel.send_target_set/v1`

Spec:

- `count`: number of base ring pairs to generate.
- `mode`: `ring` for deterministic non-self pairs.
- `bidirectional`: when false, emit `count` directed targets; when true, emit both directions for each base pair, producing `count * 2` directed targets.

The first implementation should support `ring` mode. For identity `i`, sender is `i` and recipient is `(i+1) % total`. This provides stable coverage without random state or additional server preparation.

## Result Model

Reuse `traffic.Summary` for success/error compatibility and extend it only if tests prove a report-level value is needed. Latency is already represented as the `sendack_latency` duration metric in kernel reports.

Metrics emitted by `traffic.send/v1`:

- `send_attempt_total` counter.
- `sendack_success_total` counter.
- `sendack_error_total` counter.
- `sendack_latency` duration.

Labels are optional in the first implementation. If labels are added, keep them coarse, such as `channel_type=person` and `channel_type=group`, to avoid high-cardinality channel IDs.

## Data Flow

```text
identity.pool
  -> wukongim.prepare_tokens
  -> wkproto.session_pool
      -> port.wkproto.message_sender/v1

identity.pool
  -> wukongim.prepare_group_channels
      -> port.channel.send_target_set/v1
      -> traffic.send(group workload)

identity.pool
  -> identity.person_pairs
      -> port.channel.send_target_set/v1
      -> traffic.send(person workload)

traffic.send
  -> SEND
  -> matching SENDACK
  -> counters, sendack_latency, traffic.summary
  -> report.assert and report files
```

## Error Handling

- Validation fails when `rate <= 0`, `payload_size < 0`, `max_in_flight < 0`, or `sender_pick` is unknown.
- Runtime fails when the target set is empty.
- A target with no sender uids counts as a sendack error for that operation and continues.
- A missing connected sender client counts as a sendack error for that operation and continues.
- Protocol send errors, sendack timeout, and non-success sendack reason codes count as sendack errors and continue.
- Context cancellation or run shutdown should stop the workload and return the context error.

This mirrors the current traffic behavior: data-plane errors are measured, while benchmark lifecycle cancellation is terminal.

## Testing Strategy

Use TDD for the implementation.

Port and unit tests:

- `traffic.send/v1` validates legal and illegal specs.
- `traffic.send/v1` plan reports deterministic total messages, rate, duration, payload size, sender selection, and max in flight.
- `traffic.send/v1` run uses fake ports to prove send attempts, success counters, error counters, latency samples, and summary output.
- `traffic.send/v1` sends the requested `ChannelType`, proving person and group targets share the same workload path.
- `traffic.send/v1` paces starts according to `rate` and bounds concurrent waits according to `max_in_flight`.
- `identity.person_pairs/v1` generates deterministic non-self ring pairs and rejects impossible specs.
- `wukongim.prepare_group_channels/v1` still outputs the existing group set and additionally outputs generic targets.
- `wkproto.session_pool/v1` exposes both legacy group sender and generic message sender.

Scenario tests:

- Add or update example YAML for group send using `traffic.send/v1`.
- Add a mixed example with group and person traffic units using independent rates.
- Validate and explain the mixed example without connecting to a target.

Smoke:

- Keep the existing single-node smoke path working.
- Add a real scenario path that can be run against a WuKongIM target with bench API enabled and emits both group and person send-rate summaries.

## Rollout

1. Add generic channel and WKProto ports.
2. Extend the session pool client to send arbitrary channel types.
3. Add `identity.person_pairs/v1`.
4. Add `traffic.send/v1` with real sendack latency measurement and bounded concurrency.
5. Extend group preparation to publish generic send targets.
6. Add mixed workload examples and documentation.
7. Keep existing group send examples valid during the transition.
