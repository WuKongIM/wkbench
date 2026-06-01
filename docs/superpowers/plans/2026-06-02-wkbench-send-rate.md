# wkbench Send Rate Workload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build composable `SEND -> SENDACK` rate workloads for WuKongIM, with group and person traffic units configurable side by side in one `wkbench` scenario.

**Architecture:** Add small public ports for generic send targets and generic WKProto sends, then extend existing units to produce or consume those ports. Keep the old group-send path working while adding `traffic.send/v1` as the new measured workload with pacing, bounded in-flight sends, counters, and sendack latency metrics.

**Tech Stack:** Go, `wkbench` unit contracts, WKProto frame codec, `go test`, scenario YAML.

---

## File Structure

- Create `benchkit/ports/channel/send_target.go`: public `SendTargetSetV1`, `SendTargetSet`, and `SendTarget` contracts.
- Create `benchkit/ports/channel/send_target_test.go`: compile-time and value tests for the target contract.
- Create `benchkit/ports/wkproto/message_sender.go`: public `MessageSenderV1`, `MessageSender`, `MessageClient`, and `SendRequest` contracts.
- Create `benchkit/ports/wkproto/message_sender_test.go`: compile-time and value tests for the sender contract.
- Modify `benchkit/contract/types.go`: make `TestRunEnv` record duration observations for unit tests.
- Create `benchkit/contract/types_test.go`: tests for duration observation capture.
- Modify `units/wkproto/session_pool/client.go`: add generic `SendAndWaitAck` and make group send delegate to it with channel type `2`.
- Modify `units/wkproto/session_pool/unit.go`: output both `group_sender` and `message_sender`; keep `Pool.Client` and add `Pool.MessageClient`.
- Modify `units/wkproto/session_pool/client_test.go`: prove generic sends encode the requested channel type.
- Modify `units/wkproto/session_pool/unit_test.go`: prove `message_sender` output is available.
- Create `units/identity/person_pairs/unit.go`: deterministic person target generator.
- Create `units/identity/person_pairs/unit_test.go`: validation, ring, bidirectional, and run-error tests.
- Modify `units/wukongim/prepare_group_channels/unit.go`: publish generic group send targets in addition to the existing group set.
- Modify `units/wukongim/prepare_group_channels/unit_test.go`: assert `targets` output.
- Create `units/traffic/send/unit.go`: generic paced `SEND -> SENDACK` workload.
- Create `units/traffic/send/unit_test.go`: validation, plan, send behavior, latency, channel type, pacing helper, and max-in-flight tests.
- Create `units/core/fake_message_sender/unit.go`: fake generic message sender for dry scenarios.
- Create `units/core/fake_message_sender/unit_test.go`: fake sender contract and ack tests.
- Modify `cmd/wkbench/main.go`: register `identity.person_pairs/v1` and `traffic.send/v1`.
- Modify `cmd/wkbench/main_test.go`: list and validate mixed scenario coverage.
- Create `examples/wukongim-send-rate-mixed.yaml`: real WuKongIM mixed group/person send-rate scenario.
- Modify `README.md`: document new units and example commands.

All commands below run from `/Users/tt/Desktop/work/go/WuKongIM-v2/wkbench`.

---

### Task 1: Add Generic Port Contracts

**Files:**
- Create: `benchkit/ports/channel/send_target.go`
- Create: `benchkit/ports/channel/send_target_test.go`
- Create: `benchkit/ports/wkproto/message_sender.go`
- Create: `benchkit/ports/wkproto/message_sender_test.go`

- [ ] **Step 1: Write failing tests for the channel target port**

Create `benchkit/ports/channel/send_target_test.go`:

```go
package channel_test

import (
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
)

func TestSendTargetPortContract(t *testing.T) {
	if channelport.SendTargetSetV1 != contract.PortType("port.channel.send_target_set/v1") {
		t.Fatalf("unexpected port type %q", channelport.SendTargetSetV1)
	}
	target := channelport.SendTarget{
		ChannelID:   "u2",
		ChannelType: 1,
		SenderUIDs:  []string{"u1"},
	}
	if target.ChannelID != "u2" || target.ChannelType != 1 || target.SenderUIDs[0] != "u1" {
		t.Fatalf("unexpected target: %#v", target)
	}
}
```

- [ ] **Step 2: Write failing tests for the WKProto message sender port**

Create `benchkit/ports/wkproto/message_sender_test.go`:

```go
package wkproto_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
)

func TestMessageSenderPortContract(t *testing.T) {
	if wkprotoport.MessageSenderV1 != contract.PortType("port.wkproto.message_sender/v1") {
		t.Fatalf("unexpected port type %q", wkprotoport.MessageSenderV1)
	}
	req := wkprotoport.SendRequest{
		ChannelID:   "u2",
		ChannelType: 1,
		SenderUID:   "u1",
		ClientMsgNo: "msg-1",
		Payload:     []byte("hello"),
		Timeout:     time.Second,
	}
	ack, err := fakeMessageClient{}.SendAndWaitAck(context.Background(), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ack.MessageID != 7 || ack.MessageSeq != 9 {
		t.Fatalf("unexpected ack: %#v", ack)
	}
}

type fakeMessageClient struct{}

func (fakeMessageClient) SendAndWaitAck(context.Context, wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	return wkprotoport.SendAck{MessageID: 7, MessageSeq: 9}, nil
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./benchkit/ports/channel ./benchkit/ports/wkproto
```

Expected: FAIL because `SendTargetSetV1`, `SendTarget`, `MessageSenderV1`, and `SendRequest` are undefined.

- [ ] **Step 4: Implement channel send target port**

Create `benchkit/ports/channel/send_target.go`:

```go
// Package channel defines public channel-related ports.
package channel

import "github.com/WuKongIM/wkbench/benchkit/contract"

// SendTargetSetV1 is the port type for deterministic send target sets.
const SendTargetSetV1 contract.PortType = "port.channel.send_target_set/v1"

// SendTargetSet exposes generated or discovered protocol send targets.
type SendTargetSet interface {
	// Count returns the number of send targets.
	Count() int
	// At returns the send target at index.
	At(index int) SendTarget
}

// SendTarget describes one protocol send destination and its usable senders.
type SendTarget struct {
	// ChannelID is the client-visible protocol channel id.
	ChannelID string `json:"channel_id"`
	// ChannelType is the WuKong protocol channel type.
	ChannelType uint8 `json:"channel_type"`
	// SenderUIDs are connected users allowed to send to this target.
	SenderUIDs []string `json:"sender_uids"`
}
```

- [ ] **Step 5: Implement generic WKProto message sender port**

Create `benchkit/ports/wkproto/message_sender.go`:

```go
// Package wkproto defines WKProto capability ports.
package wkproto

import (
	"context"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

// MessageSenderV1 is the port type for sending protocol messages.
const MessageSenderV1 contract.PortType = "port.wkproto.message_sender/v1"

// MessageSender returns message-capable clients by uid.
type MessageSender interface {
	// MessageClient returns the connected message client for uid.
	MessageClient(uid string) (MessageClient, bool)
}

// MessageClient sends one protocol message and waits for sendack.
type MessageClient interface {
	// SendAndWaitAck sends req and waits for the matching sendack.
	SendAndWaitAck(ctx context.Context, req SendRequest) (SendAck, error)
}

// SendRequest describes one protocol send operation.
type SendRequest struct {
	// ChannelID is the target channel id.
	ChannelID string
	// ChannelType is the target channel type.
	ChannelType uint8
	// SenderUID is the sending user id.
	SenderUID string
	// ClientMsgNo is the deterministic client message number.
	ClientMsgNo string
	// Payload is the message payload.
	Payload []byte
	// Timeout bounds waiting for sendack.
	Timeout time.Duration
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run:

```bash
GOWORK=off go test ./benchkit/ports/channel ./benchkit/ports/wkproto
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add benchkit/ports/channel/send_target.go benchkit/ports/channel/send_target_test.go benchkit/ports/wkproto/message_sender.go benchkit/ports/wkproto/message_sender_test.go
git commit -m "feat: add generic send ports"
```

---

### Task 2: Record Duration Observations in TestRunEnv

**Files:**
- Modify: `benchkit/contract/types.go`
- Create: `benchkit/contract/types_test.go`

- [ ] **Step 1: Write failing test**

Create `benchkit/contract/types_test.go`:

```go
package contract

import (
	"testing"
	"time"
)

func TestTestRunEnvRecordsDurationObservations(t *testing.T) {
	env := NewTestRunEnv("run-1", "traffic", nil, nil)
	env.ObserveDuration("sendack_latency", 10*time.Millisecond, nil)
	env.ObserveDuration("sendack_latency", 20*time.Millisecond, nil)

	values := env.DurationValues("sendack_latency")
	if len(values) != 2 || values[0] != 10*time.Millisecond || values[1] != 20*time.Millisecond {
		t.Fatalf("unexpected duration values: %#v", values)
	}
	values[0] = time.Hour
	if env.DurationValues("sendack_latency")[0] != 10*time.Millisecond {
		t.Fatal("DurationValues must return a copy")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
GOWORK=off go test ./benchkit/contract
```

Expected: FAIL because `DurationValues` is undefined and `ObserveDuration` does not store samples.

- [ ] **Step 3: Implement duration capture**

Modify `benchkit/contract/types.go`:

```go
type TestRunEnv struct {
	runID       string
	unitName    string
	inputs      map[string]any
	spec        map[string]any
	outputs     map[string]any
	counters    map[string]float64
	durations   map[string][]time.Duration
	runDuration time.Duration

	mu     sync.Mutex
	nextID int64
}
```

In `NewTestRunEnv`, initialize `durations`:

```go
durations:   make(map[string][]time.Duration),
```

Replace `ObserveDuration` and add `DurationValues`:

```go
// ObserveDuration implements RunEnv.
func (e *TestRunEnv) ObserveDuration(name string, value time.Duration, labels Labels) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.durations[name] = append(e.durations[name], value)
}

// DurationValues returns recorded duration samples for name.
func (e *TestRunEnv) DurationValues(name string) []time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	values := e.durations[name]
	return append([]time.Duration(nil), values...)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
GOWORK=off go test ./benchkit/contract
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add benchkit/contract/types.go benchkit/contract/types_test.go
git commit -m "test: record duration samples in test env"
```

---

### Task 3: Extend Session Pool With Generic Message Sender

**Files:**
- Modify: `units/wkproto/session_pool/client.go`
- Modify: `units/wkproto/session_pool/client_test.go`
- Modify: `units/wkproto/session_pool/unit.go`
- Modify: `units/wkproto/session_pool/unit_test.go`

- [ ] **Step 1: Write failing unit-output test**

In `units/wkproto/session_pool/unit_test.go`, update `TestSessionPoolConnectsIdentitiesAndOutputsGroupSender` to also assert `message_sender`:

```go
	messageSender, err := contract.Output[wkprotoport.MessageSender](env, "message_sender")
	if err != nil {
		t.Fatalf("message_sender output: %v", err)
	}
	messageClient, ok := messageSender.MessageClient("u2")
	if !ok {
		t.Fatal("expected message client for u2")
	}
	ack, err := messageClient.SendAndWaitAck(context.Background(), wkprotoport.SendRequest{
		ChannelID:   "u1",
		ChannelType: 1,
		SenderUID:   "u2",
		ClientMsgNo: "msg-1",
		Payload:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("generic send: %v", err)
	}
	if ack.MessageID != 1 {
		t.Fatalf("unexpected generic send ack: %#v", ack)
	}
```

Add this method to the test `fakeClient`:

```go
func (fakeClient) SendAndWaitAck(context.Context, wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	return wkprotoport.SendAck{MessageID: 1}, nil
}
```

- [ ] **Step 2: Write failing protocol-client test**

In `units/wkproto/session_pool/client_test.go`, add:

```go
func TestSendAndWaitAckUsesRequestedChannelType(t *testing.T) {
	proto := protocolcodec.New()
	ackBytes, err := proto.EncodeFrame(&frame.SendackPacket{
		ClientSeq:   1,
		ClientMsgNo: "msg-1",
		ReasonCode:  frame.ReasonSuccess,
		MessageID:   42,
		MessageSeq:  3,
	}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("encode ack: %v", err)
	}
	conn := &bufferConn{read: bytes.NewReader(ackBytes)}
	client := &wkClient{conn: conn, proto: proto, operationTimeout: time.Second}

	ack, err := client.SendAndWaitAck(context.Background(), wkprotoport.SendRequest{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		SenderUID:   "u1",
		ClientMsgNo: "msg-1",
		Payload:     []byte("hello"),
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ack.MessageID != 42 || ack.MessageSeq != 3 {
		t.Fatalf("unexpected ack: %#v", ack)
	}
	written := conn.written.Bytes()
	decoded, _, err := proto.DecodeFrame(written, frame.LatestVersion)
	if err != nil {
		t.Fatalf("decode written frame: %v", err)
	}
	send, ok := decoded.(*frame.SendPacket)
	if !ok {
		t.Fatalf("expected send packet, got %T", decoded)
	}
	if send.ChannelID != "u2" || send.ChannelType != frame.ChannelTypePerson || send.ClientMsgNo != "msg-1" {
		t.Fatalf("unexpected send packet: %#v", send)
	}
}

type bufferConn struct {
	read    *bytes.Reader
	written bytes.Buffer
}

func (c *bufferConn) Read(p []byte) (int, error)         { return c.read.Read(p) }
func (c *bufferConn) Write(p []byte) (int, error)        { return c.written.Write(p) }
func (c *bufferConn) Close() error                       { return nil }
func (c *bufferConn) LocalAddr() net.Addr                { return dummyAddr("local") }
func (c *bufferConn) RemoteAddr() net.Addr               { return dummyAddr("remote") }
func (c *bufferConn) SetDeadline(time.Time) error        { return nil }
func (c *bufferConn) SetReadDeadline(time.Time) error    { return nil }
func (c *bufferConn) SetWriteDeadline(time.Time) error   { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }
```

Also add imports in `client_test.go`:

```go
	"bytes"
	"net"

	protocolcodec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./units/wkproto/session_pool
```

Expected: FAIL because `SendAndWaitAck`, `MessageSender`, and `message_sender` output are missing.

- [ ] **Step 4: Implement generic client send**

In `units/wkproto/session_pool/client.go`, add an operation mutex to `wkClient` so a single TCP session never has two goroutines reading sendacks from the same connection:

```go
type wkClient struct {
	addr   string
	dialer interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
	operationTimeout time.Duration
	proto            *protocolcodec.WKProto
	token            string

	mu            sync.Mutex
	writeMu       sync.Mutex
	opMu          sync.Mutex
	conn          net.Conn
	privateKey    [32]byte
	publicKey     [32]byte
	sessionCrypto *protocolenc.SessionCrypto
	clientSeq     uint64
}
```

Then replace `SendGroupAndWaitAck` with a wrapper plus generic method:

```go
func (c *wkClient) SendGroupAndWaitAck(ctx context.Context, req wkprotoport.GroupSendRequest) (wkprotoport.SendAck, error) {
	return c.SendAndWaitAck(ctx, wkprotoport.SendRequest{
		ChannelID:   req.ChannelID,
		ChannelType: 2,
		SenderUID:   req.SenderUID,
		ClientMsgNo: req.ClientMsgNo,
		Payload:     req.Payload,
		Timeout:     req.Timeout,
	})
}

func (c *wkClient) SendAndWaitAck(ctx context.Context, req wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	ctx, cancel := c.withRequestTimeout(ctx, req.Timeout)
	defer cancel()
	c.clientSeq++
	send := &frame.SendPacket{
		ClientSeq:   c.clientSeq,
		ClientMsgNo: req.ClientMsgNo,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
		Payload:     req.Payload,
	}
	if err := c.send(ctx, send); err != nil {
		return wkprotoport.SendAck{}, err
	}
	for {
		f, err := c.readFrame(ctx)
		if err != nil {
			return wkprotoport.SendAck{}, err
		}
		ack, ok := f.(*frame.SendackPacket)
		if !ok {
			continue
		}
		if !sendackMatchesRequest(ack, req.ClientMsgNo, send.ClientSeq) {
			continue
		}
		if ack.ReasonCode != frame.ReasonSuccess {
			return wkprotoport.SendAck{}, fmt.Errorf("sendack reason code %s", ack.ReasonCode)
		}
		return wkprotoport.SendAck{MessageID: ack.MessageID, MessageSeq: ack.MessageSeq}, nil
	}
}
```

- [ ] **Step 5: Implement session pool output**

In `units/wkproto/session_pool/unit.go`, extend the `Client` interface:

```go
type Client interface {
	wkprotoport.GroupClient
	wkprotoport.MessageClient
	// Close releases the underlying session.
	Close() error
}
```

Add the output definition:

```go
Outputs: []contract.PortDef{
	{Name: "group_sender", Type: wkprotoport.GroupSenderV1},
	{Name: "message_sender", Type: wkprotoport.MessageSenderV1},
},
```

In `Run`, set both outputs:

```go
if err := env.SetOutput("group_sender", pool); err != nil {
	return err
}
return env.SetOutput("message_sender", pool)
```

Add `MessageClient` to `Pool`:

```go
// MessageClient implements wkproto.MessageSender.
func (p *Pool) MessageClient(uid string) (wkprotoport.MessageClient, bool) {
	client, ok := p.clients[uid]
	return client, ok
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run:

```bash
GOWORK=off go test ./units/wkproto/session_pool
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add units/wkproto/session_pool/client.go units/wkproto/session_pool/client_test.go units/wkproto/session_pool/unit.go units/wkproto/session_pool/unit_test.go
git commit -m "feat: expose generic wkproto sender"
```

---

### Task 4: Add Deterministic Person Pair Targets

**Files:**
- Create: `units/identity/person_pairs/unit.go`
- Create: `units/identity/person_pairs/unit_test.go`

- [ ] **Step 1: Write failing tests**

Create `units/identity/person_pairs/unit_test.go`:

```go
package personpairs_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
	personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, personpairs.Unit{})
}

func TestPersonPairsGenerateRingTargets(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "pairs", map[string]any{
		"identities": identityPool{items: []identityport.Identity{{UID: "u1"}, {UID: "u2"}, {UID: "u3"}}},
	}, map[string]any{
		"count": 2,
		"mode":  "ring",
	})

	if err := (personpairs.Unit{}).Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := (personpairs.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	targets, err := contract.Output[channelport.SendTargetSet](env, "targets")
	if err != nil {
		t.Fatalf("targets output: %v", err)
	}
	if targets.Count() != 2 {
		t.Fatalf("target count = %d, want 2", targets.Count())
	}
	first := targets.At(0)
	second := targets.At(1)
	if first.ChannelID != "u2" || first.ChannelType != 1 || first.SenderUIDs[0] != "u1" {
		t.Fatalf("unexpected first target: %#v", first)
	}
	if second.ChannelID != "u3" || second.ChannelType != 1 || second.SenderUIDs[0] != "u2" {
		t.Fatalf("unexpected second target: %#v", second)
	}
}

func TestPersonPairsBidirectionalExpandsEachPair(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "pairs", map[string]any{
		"identities": identityPool{items: []identityport.Identity{{UID: "u1"}, {UID: "u2"}}},
	}, map[string]any{
		"count":         1,
		"mode":          "ring",
		"bidirectional": true,
	})

	if err := (personpairs.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	targets, err := contract.Output[channelport.SendTargetSet](env, "targets")
	if err != nil {
		t.Fatalf("targets output: %v", err)
	}
	if targets.Count() != 2 {
		t.Fatalf("target count = %d, want 2", targets.Count())
	}
	if targets.At(0).ChannelID != "u2" || targets.At(0).SenderUIDs[0] != "u1" {
		t.Fatalf("unexpected forward target: %#v", targets.At(0))
	}
	if targets.At(1).ChannelID != "u1" || targets.At(1).SenderUIDs[0] != "u2" {
		t.Fatalf("unexpected reverse target: %#v", targets.At(1))
	}
}

func TestPersonPairsValidateRejectsInvalidSpec(t *testing.T) {
	for name, spec := range map[string]map[string]any{
		"missing count": {"mode": "ring"},
		"unknown mode":  {"count": 1, "mode": "random"},
	} {
		t.Run(name, func(t *testing.T) {
			env := contract.NewTestRunEnv("run-1", "pairs", nil, spec)
			if err := (personpairs.Unit{}).Validate(context.Background(), env); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestPersonPairsRunRejectsTooFewIdentities(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "pairs", map[string]any{
		"identities": identityPool{items: []identityport.Identity{{UID: "u1"}}},
	}, map[string]any{"count": 1, "mode": "ring"})

	if err := (personpairs.Unit{}).Run(context.Background(), env); err == nil {
		t.Fatal("expected run error")
	}
}

type identityPool struct {
	items []identityport.Identity
}

func (p identityPool) Count() int { return len(p.items) }
func (p identityPool) At(index int) identityport.Identity { return p.items[index] }
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./units/identity/person_pairs
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the unit**

Create `units/identity/person_pairs/unit.go`:

```go
// Package personpairs implements identity.person_pairs/v1.
package personpairs

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const (
	kind              = "identity.person_pairs/v1"
	modeRing          = "ring"
	personChannelType = uint8(1)
)

// Unit produces deterministic person send targets.
type Unit struct{}

// Spec configures deterministic person pair generation.
type Spec struct {
	// Count is the number of base ring pairs to generate.
	Count int `json:"count" yaml:"count"`
	// Mode selects the pair generation strategy.
	Mode string `json:"mode" yaml:"mode"`
	// Bidirectional emits both directions for each base pair.
	Bidirectional bool `json:"bidirectional" yaml:"bidirectional"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Person send pairs",
		Description: "Produces deterministic person-channel send targets from an identity pool.",
		Inputs: []contract.PortDef{
			{Name: "identities", Type: identityport.PoolV1},
		},
		Outputs: []contract.PortDef{
			{Name: "targets", Type: channelport.SendTargetSetV1},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	if spec.Count <= 0 {
		return fmt.Errorf("count must be greater than zero")
	}
	if spec.Mode != modeRing {
		return fmt.Errorf("unsupported mode %q", spec.Mode)
	}
	return nil
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	pool, err := contract.Input[identityport.Pool](env, "identities")
	if err != nil {
		return err
	}
	if pool.Count() < 2 {
		return fmt.Errorf("person_pairs: at least two identities are required")
	}
	targets := make([]channelport.SendTarget, 0, spec.Count)
	if spec.Bidirectional {
		targets = make([]channelport.SendTarget, 0, spec.Count*2)
	}
	for i := 0; i < spec.Count; i++ {
		sender := pool.At(i % pool.Count()).UID
		recipient := pool.At((i + 1) % pool.Count()).UID
		targets = append(targets, personTarget(sender, recipient))
		if spec.Bidirectional {
			targets = append(targets, personTarget(recipient, sender))
		}
	}
	return env.SetOutput("targets", TargetSet{Items: targets})
}

func decodeSpec(env contract.ValidateEnv) (Spec, error) {
	spec := Spec{Mode: modeRing}
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	spec.Mode = strings.TrimSpace(spec.Mode)
	if spec.Mode == "" {
		spec.Mode = modeRing
	}
	return spec, nil
}

func personTarget(senderUID, recipientUID string) channelport.SendTarget {
	return channelport.SendTarget{
		ChannelID:   recipientUID,
		ChannelType: personChannelType,
		SenderUIDs:  []string{senderUID},
	}
}

// TargetSet is a JSON-friendly send target set.
type TargetSet struct {
	// Items contains generated send targets.
	Items []channelport.SendTarget `json:"items"`
}

// Count implements channel.SendTargetSet.
func (s TargetSet) Count() int { return len(s.Items) }

// At implements channel.SendTargetSet.
func (s TargetSet) At(index int) channelport.SendTarget { return s.Items[index] }
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
GOWORK=off go test ./units/identity/person_pairs
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add units/identity/person_pairs/unit.go units/identity/person_pairs/unit_test.go
git commit -m "feat: add person send targets"
```

---

### Task 5: Publish Group Send Targets

**Files:**
- Modify: `units/wukongim/prepare_group_channels/unit.go`
- Modify: `units/wukongim/prepare_group_channels/unit_test.go`

- [ ] **Step 1: Write failing test assertion**

In `units/wukongim/prepare_group_channels/unit_test.go`, after the existing `groups` output assertion, add:

```go
	targets, err := contract.Output[channelport.SendTargetSet](env, "targets")
	if err != nil {
		t.Fatalf("targets output: %v", err)
	}
	if targets.Count() != 2 {
		t.Fatalf("unexpected target count: %d", targets.Count())
	}
	firstTarget := targets.At(0)
	if firstTarget.ChannelID != "run-1-small-0" || firstTarget.ChannelType != 2 || len(firstTarget.SenderUIDs) != 2 || firstTarget.SenderUIDs[0] != "u1" {
		t.Fatalf("unexpected first target: %#v", firstTarget)
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./units/wukongim/prepare_group_channels
```

Expected: FAIL because output `targets` is not declared or produced.

- [ ] **Step 3: Add output declaration and target set**

In `Definition`, add the new output:

```go
Outputs: []contract.PortDef{
	{Name: "channels", Type: channelport.GroupSetV1},
	{Name: "targets", Type: channelport.SendTargetSetV1},
},
```

After successful bench API calls in `Run`, set both outputs:

```go
if err := env.SetOutput("channels", groups); err != nil {
	return err
}
return env.SetOutput("targets", targetsFromGroups(groups))
```

Add helper and concrete set:

```go
func targetsFromGroups(groups GroupSet) TargetSet {
	targets := make([]channelport.SendTarget, 0, len(groups.Items))
	for _, group := range groups.Items {
		targets = append(targets, channelport.SendTarget{
			ChannelID:   group.ChannelID,
			ChannelType: groupChannelType,
			SenderUIDs:  append([]string(nil), group.Members...),
		})
	}
	return TargetSet{Items: targets}
}

// TargetSet is a JSON-friendly prepared send target set.
type TargetSet struct {
	// Items contains prepared send targets.
	Items []channelport.SendTarget `json:"items"`
}

// Count implements channel.SendTargetSet.
func (s TargetSet) Count() int { return len(s.Items) }

// At implements channel.SendTargetSet.
func (s TargetSet) At(index int) channelport.SendTarget { return s.Items[index] }
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
GOWORK=off go test ./units/wukongim/prepare_group_channels
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add units/wukongim/prepare_group_channels/unit.go units/wukongim/prepare_group_channels/unit_test.go
git commit -m "feat: publish group send targets"
```

---

### Task 6: Add Generic `traffic.send/v1`

**Files:**
- Create: `units/traffic/send/unit.go`
- Create: `units/traffic/send/unit_test.go`

- [ ] **Step 1: Write failing tests**

Create `units/traffic/send/unit_test.go`:

```go
package send_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
	sendunit "github.com/WuKongIM/wkbench/units/traffic/send"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, sendunit.Unit{})
}

func TestSendUsesTargetsAndEmitsSummaryAndLatency(t *testing.T) {
	client := &recordingClient{}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "group-1", ChannelType: 2, SenderUIDs: []string{"u1", "u2"}},
			{ChannelID: "u3", ChannelType: 1, SenderUIDs: []string{"u2"}},
		}},
		"sender": messageSender{client: client},
	}, map[string]any{
		"rate":         "2000/s",
		"payload_size": 16,
		"sender_pick":  "round_robin",
		"ack_timeout":  "1s",
	})
	env.SetRunDuration(time.Millisecond)

	unit := sendunit.Unit{}
	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	summary, err := contract.Output[trafficport.Summary](env, "summary")
	if err != nil {
		t.Fatalf("summary output: %v", err)
	}
	if summary.SendackOK != 2 || summary.SendackErrors != 0 || summary.LastMessageID != 2 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if got := env.CounterValue("send_attempt_total"); got != 2 {
		t.Fatalf("attempts = %v, want 2", got)
	}
	if got := env.CounterValue("sendack_success_total"); got != 2 {
		t.Fatalf("successes = %v, want 2", got)
	}
	if got := env.CounterValue("sendack_error_total"); got != 0 {
		t.Fatalf("errors = %v, want 0", got)
	}
	if got := len(env.DurationValues("sendack_latency")); got != 2 {
		t.Fatalf("latency sample count = %d, want 2", got)
	}
	if len(client.requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(client.requests))
	}
	if client.requests[0].ChannelID != "group-1" || client.requests[0].ChannelType != 2 || client.requests[0].SenderUID != "u1" {
		t.Fatalf("unexpected first request: %#v", client.requests[0])
	}
	if client.requests[1].ChannelID != "u3" || client.requests[1].ChannelType != 1 || client.requests[1].SenderUID != "u2" {
		t.Fatalf("unexpected second request: %#v", client.requests[1])
	}
}

func TestSendRecordsErrorsAndContinues(t *testing.T) {
	client := &recordingClient{errOnCall: 2}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "g1", ChannelType: 2, SenderUIDs: []string{"u1"}},
			{ChannelID: "g2", ChannelType: 2, SenderUIDs: []string{"u1"}},
		}},
		"sender": messageSender{client: client},
	}, map[string]any{"rate": "2000/s"})
	env.SetRunDuration(time.Millisecond)

	if err := (sendunit.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	summary, err := contract.Output[trafficport.Summary](env, "summary")
	if err != nil {
		t.Fatalf("summary output: %v", err)
	}
	if summary.SendackOK != 1 || summary.SendackErrors != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestSendPlanReportsDeterministicShard(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "traffic", nil, map[string]any{
		"rate":          "2.5/s",
		"payload_size":  32,
		"sender_pick":   "round_robin",
		"max_in_flight": 8,
	})
	env.SetRunDuration(2 * time.Second)

	plan, err := (sendunit.Unit{}).Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	data, err := json.Marshal(plan.Shards[0])
	if err != nil {
		t.Fatalf("marshal shard: %v", err)
	}
	var shard struct {
		TotalMessages int64   `json:"total_messages"`
		RatePerSecond float64 `json:"rate_per_second"`
		DurationMS    int64   `json:"duration_ms"`
		PayloadSize   int     `json:"payload_size"`
		SenderPick    string  `json:"sender_pick"`
		MaxInFlight   int     `json:"max_in_flight"`
	}
	if err := json.Unmarshal(data, &shard); err != nil {
		t.Fatalf("unmarshal shard: %v", err)
	}
	if shard.TotalMessages != 5 || shard.RatePerSecond != 2.5 || shard.DurationMS != 2000 || shard.PayloadSize != 32 || shard.SenderPick != "round_robin" || shard.MaxInFlight != 8 {
		t.Fatalf("unexpected shard: %#v", shard)
	}
}

func TestSendValidateRejectsInvalidSpec(t *testing.T) {
	for name, spec := range map[string]map[string]any{
		"zero rate":      {"rate": "0/s"},
		"bad payload":    {"rate": "1/s", "payload_size": -1},
		"bad in flight":  {"rate": "1/s", "max_in_flight": -1},
		"bad sender pick": {"rate": "1/s", "sender_pick": "random"},
	} {
		t.Run(name, func(t *testing.T) {
			env := contract.NewTestRunEnv("run-1", "traffic", nil, spec)
			if err := (sendunit.Unit{}).Validate(context.Background(), env); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestScheduledStart(t *testing.T) {
	start := time.Unix(100, 0)
	got := sendunit.ScheduledStartForTest(start, contract.Rate{PerSecond: 4}, 3)
	want := start.Add(750 * time.Millisecond)
	if !got.Equal(want) {
		t.Fatalf("scheduled start = %s, want %s", got, want)
	}
}

func TestSendHonorsMaxInFlight(t *testing.T) {
	client := &recordingClient{delay: 2 * time.Millisecond}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "g1", ChannelType: 2, SenderUIDs: []string{"u1"}},
		}},
		"sender": messageSender{client: client},
	}, map[string]any{
		"rate":          "100000/s",
		"max_in_flight": 2,
	})
	env.SetRunDuration(50 * time.Microsecond)

	if err := (sendunit.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	if client.maxActive > 2 {
		t.Fatalf("max active = %d, want <= 2", client.maxActive)
	}
}

type targetSet struct {
	items []channelport.SendTarget
}

func (s targetSet) Count() int { return len(s.items) }
func (s targetSet) At(index int) channelport.SendTarget { return s.items[index] }

type messageSender struct {
	client *recordingClient
}

func (s messageSender) MessageClient(uid string) (wkprotoport.MessageClient, bool) {
	if uid == "" {
		return nil, false
	}
	return s.client, true
}

type recordingClient struct {
	mu        sync.Mutex
	requests  []wkprotoport.SendRequest
	calls     int
	errOnCall int
	delay     time.Duration
	active    int
	maxActive int
}

func (c *recordingClient) SendAndWaitAck(ctx context.Context, req wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.requests = append(c.requests, req)
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.mu.Unlock()

	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return wkprotoport.SendAck{}, ctx.Err()
		}
	}

	c.mu.Lock()
	c.active--
	c.mu.Unlock()

	if c.errOnCall == call {
		return wkprotoport.SendAck{}, errors.New("send failed")
	}
	return wkprotoport.SendAck{MessageID: int64(call), MessageSeq: uint64(call)}, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./units/traffic/send
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement `traffic.send/v1`**

Create `units/traffic/send/unit.go` with these exported and internal pieces:

```go
// Package send implements traffic.send/v1.
package send

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const (
	kind                  = "traffic.send/v1"
	defaultAckTimeout     = 5 * time.Second
	senderPickFirstOnline = "first_online"
	senderPickRoundRobin  = "round_robin"
)

// Unit sends protocol messages and waits for sendack.
type Unit struct{}

// Spec configures traffic.send/v1.
type Spec struct {
	// Rate is the total offered send rate.
	Rate contract.Rate `json:"rate" yaml:"rate"`
	// PayloadSize is the deterministic payload size in bytes.
	PayloadSize int `json:"payload_size" yaml:"payload_size"`
	// SenderPick selects which allowed sender sends each message.
	SenderPick string `json:"sender_pick" yaml:"sender_pick"`
	// MaxInFlight bounds concurrent SEND -> SENDACK operations.
	MaxInFlight int `json:"max_in_flight" yaml:"max_in_flight"`
	// AckTimeout bounds each sendack wait.
	AckTimeout contract.Duration `json:"ack_timeout" yaml:"ack_timeout"`
}

type planShard struct {
	TotalMessages int64   `json:"total_messages"`
	RatePerSecond float64 `json:"rate_per_second"`
	DurationMS    int64   `json:"duration_ms"`
	PayloadSize   int     `json:"payload_size"`
	SenderPick    string  `json:"sender_pick,omitempty"`
	MaxInFlight   int     `json:"max_in_flight,omitempty"`
}
```

Then add the unit contract methods:

```go
// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "SEND traffic",
		Description: "Sends protocol messages and waits for matching SENDACK packets.",
		Inputs: []contract.PortDef{
			{Name: "targets", Type: channelport.SendTargetSetV1},
			{Name: "sender", Type: wkprotoport.MessageSenderV1},
		},
		Outputs: []contract.PortDef{
			{Name: "summary", Type: trafficport.SummaryV1},
		},
		Metrics: []contract.MetricDef{
			{Name: "send_attempt_total", Type: "counter"},
			{Name: "sendack_success_total", Type: "counter"},
			{Name: "sendack_error_total", Type: "counter"},
			{Name: "sendack_latency", Type: "duration"},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	if spec.Rate.PerSecond <= 0 {
		return fmt.Errorf("rate must be greater than zero")
	}
	if spec.PayloadSize < 0 {
		return fmt.Errorf("payload_size must not be negative")
	}
	if spec.MaxInFlight < 0 {
		return fmt.Errorf("max_in_flight must not be negative")
	}
	switch spec.SenderPick {
	case "", senderPickFirstOnline, senderPickRoundRobin:
		return nil
	default:
		return fmt.Errorf("unsupported sender_pick %q", spec.SenderPick)
	}
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	spec, err := decodeSpec(env)
	if err != nil {
		return contract.Plan{}, err
	}
	return contract.Plan{
		UnitName: env.UnitName(),
		Shards: []any{planShard{
			TotalMessages: totalMessages(spec.Rate, env.RunDuration()),
			RatePerSecond: spec.Rate.PerSecond,
			DurationMS:    env.RunDuration().Milliseconds(),
			PayloadSize:   spec.PayloadSize,
			SenderPick:    spec.SenderPick,
			MaxInFlight:   spec.MaxInFlight,
		}},
	}, nil
}
```

Add the runtime:

```go
// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	targets, err := contract.Input[channelport.SendTargetSet](env, "targets")
	if err != nil {
		return err
	}
	sender, err := contract.Input[wkprotoport.MessageSender](env, "sender")
	if err != nil {
		return err
	}
	if targets.Count() <= 0 {
		return fmt.Errorf("send: targets input is empty")
	}
	messageCount := totalMessages(spec.Rate, env.RunDuration())
	ackTimeout := spec.AckTimeout.Duration
	if ackTimeout <= 0 {
		ackTimeout = defaultAckTimeout
	}
	maxInFlight := spec.MaxInFlight
	if maxInFlight <= 0 {
		maxInFlight = 1
	}
	return runPaced(ctx, env, spec, ackTimeout, maxInFlight, messageCount, targets, sender)
}
```

Add pacing and result handling:

```go
type sendResult struct {
	ack     wkprotoport.SendAck
	latency time.Duration
	err     error
}

func runPaced(ctx context.Context, env contract.RunEnv, spec Spec, ackTimeout time.Duration, maxInFlight int, messageCount int64, targets channelport.SendTargetSet, sender wkprotoport.MessageSender) error {
	sem := make(chan struct{}, maxInFlight)
	results := make(chan sendResult, maxInFlight)
	start := time.Now()
	var summary trafficport.Summary
	var launched int64
	completed := int64(0)
	for completed < messageCount {
		if launched < messageCount {
			due := scheduledStart(start, spec.Rate, launched)
			if wait := time.Until(due); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case result := <-results:
					completed++
					recordResult(env, &summary, result)
					timer.Stop()
					continue
				case <-timer.C:
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case result := <-results:
				completed++
				recordResult(env, &summary, result)
				continue
			case sem <- struct{}{}:
				idx := launched
				launched++
				env.EmitCounter("send_attempt_total", 1, nil)
				go func() {
					defer func() { <-sem }()
					results <- sendOne(ctx, env, spec, ackTimeout, targets, sender, idx)
				}()
				continue
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-results:
			completed++
			recordResult(env, &summary, result)
		}
	}
	return env.SetOutput("summary", summary)
}

func recordResult(env contract.RunEnv, summary *trafficport.Summary, result sendResult) {
	if result.err != nil {
		env.EmitCounter("sendack_error_total", 1, nil)
		summary.SendackErrors++
		return
	}
	env.EmitCounter("sendack_success_total", 1, nil)
	env.ObserveDuration("sendack_latency", result.latency, nil)
	summary.SendackOK++
	summary.LastMessageID = result.ack.MessageID
}
```

Add helpers:

```go
func totalMessages(rate contract.Rate, duration time.Duration) int64 {
	total := int64(math.Round(rate.PerSecond * duration.Seconds()))
	if total < 1 {
		return 1
	}
	return total
}

func decodeSpec(env contract.ValidateEnv) (Spec, error) {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	spec.SenderPick = strings.TrimSpace(spec.SenderPick)
	return spec, nil
}

func sendOne(ctx context.Context, env contract.RunEnv, spec Spec, ackTimeout time.Duration, targets channelport.SendTargetSet, sender wkprotoport.MessageSender, msgIndex int64) sendResult {
	target := targets.At(int(msgIndex % int64(targets.Count())))
	if len(target.SenderUIDs) == 0 {
		return sendResult{err: fmt.Errorf("send: target %q has no senders", target.ChannelID)}
	}
	senderUID := pickSender(target, spec.SenderPick, msgIndex)
	client, ok := sender.MessageClient(senderUID)
	if !ok {
		return sendResult{err: fmt.Errorf("send: missing sender client %q", senderUID)}
	}
	start := time.Now()
	ack, err := client.SendAndWaitAck(ctx, wkprotoport.SendRequest{
		ChannelID:   target.ChannelID,
		ChannelType: target.ChannelType,
		SenderUID:   senderUID,
		ClientMsgNo: env.NextID("msg"),
		Payload:     env.Payload(spec.PayloadSize),
		Timeout:     ackTimeout,
	})
	if err != nil {
		return sendResult{err: err}
	}
	return sendResult{ack: ack, latency: time.Since(start)}
}

func pickSender(target channelport.SendTarget, mode string, msgIndex int64) string {
	if mode == senderPickRoundRobin {
		return target.SenderUIDs[int(msgIndex%int64(len(target.SenderUIDs)))]
	}
	return target.SenderUIDs[0]
}

func scheduledStart(start time.Time, rate contract.Rate, index int64) time.Time {
	if rate.PerSecond <= 0 {
		return start
	}
	offset := time.Duration(float64(time.Second) * float64(index) / rate.PerSecond)
	return start.Add(offset)
}

// ScheduledStartForTest exposes pacing math to package tests.
func ScheduledStartForTest(start time.Time, rate contract.Rate, index int64) time.Time {
	return scheduledStart(start, rate, index)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
GOWORK=off go test ./units/traffic/send
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add units/traffic/send/unit.go units/traffic/send/unit_test.go
git commit -m "feat: add generic send traffic"
```

---

### Task 7: Register Units and Add Mixed Scenario

**Files:**
- Create: `units/core/fake_message_sender/unit.go`
- Create: `units/core/fake_message_sender/unit_test.go`
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`
- Create: `examples/wukongim-send-rate-mixed.yaml`

- [ ] **Step 1: Write failing fake sender tests**

Create `units/core/fake_message_sender/unit_test.go`:

```go
package fakemessagesender_test

import (
	"context"
	"testing"

	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
	fakemessagesender "github.com/WuKongIM/wkbench/units/core/fake_message_sender"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, fakemessagesender.Unit{})
}

func TestFakeMessageSenderAcknowledgesSends(t *testing.T) {
	sender := &fakemessagesender.Sender{}
	client, ok := sender.MessageClient("u1")
	if !ok {
		t.Fatal("expected client")
	}
	ack, err := client.SendAndWaitAck(context.Background(), wkprotoport.SendRequest{
		ChannelID:   "u2",
		ChannelType: 1,
		SenderUID:   "u1",
		ClientMsgNo: "msg-1",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ack.MessageID != 1 || ack.MessageSeq != 1 {
		t.Fatalf("unexpected ack: %#v", ack)
	}
}
```

- [ ] **Step 2: Run fake sender tests to verify they fail**

Run:

```bash
GOWORK=off go test ./units/core/fake_message_sender
```

Expected: FAIL because `core.fake_message_sender` has no implementation.

- [ ] **Step 3: Write failing CLI tests**

In `cmd/wkbench/main_test.go`, add `identity.person_pairs/v1` and `traffic.send/v1` to `TestListUnitsIncludesWuKongIMBlackBoxUnits`:

```go
		"identity.person_pairs/v1",
		"traffic.send/v1",
```

Add a mixed scenario validation test:

```go
func TestValidateCommandAcceptsMixedSendRateScenario(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: mixed-send-rate
  duration: 1ms
units:
  identities:
    use: identity.pool
    spec:
      total: 4
      uid_prefix: u
      device_prefix: d
  pairs:
    use: identity.person_pairs
    spec:
      count: 2
      mode: ring
  sender:
    use: core.fake_message_sender
  person_traffic:
    use: traffic.send
    inputs:
      targets: pairs.targets
      sender: sender.sender
    spec:
      rate: 1000/s
      payload_size: 8
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"validate", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
}
```

This test intentionally references `core.fake_message_sender`, which is implemented in Step 5.

- [ ] **Step 4: Run CLI tests to verify they fail**

Run:

```bash
GOWORK=off go test ./cmd/wkbench
```

Expected: FAIL because new units and `core.fake_message_sender` are not registered.

- [ ] **Step 5: Implement fake message sender**

Create `units/core/fake_message_sender/unit.go`:

```go
// Package fakemessagesender provides a deterministic in-memory message sender.
package fakemessagesender

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const kind = "core.fake_message_sender/v1"

// Unit produces a fake message sender for dry-run examples.
type Unit struct{}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Fake message sender",
		Description: "Produces a deterministic in-memory sender that accepts every protocol send.",
		Outputs: []contract.PortDef{
			{Name: "sender", Type: wkprotoport.MessageSenderV1},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(context.Context, contract.ValidateEnv) error { return nil }

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	return env.SetOutput("sender", &Sender{})
}

// Sender returns a fake client for any uid.
type Sender struct {
	next int64
}

// MessageClient implements wkproto.MessageSender.
func (s *Sender) MessageClient(uid string) (wkprotoport.MessageClient, bool) {
	if uid == "" {
		return nil, false
	}
	return &Client{sender: s}, true
}

// Client acknowledges every send with a monotonically increasing message id.
type Client struct {
	sender *Sender
}

// SendAndWaitAck implements wkproto.MessageClient.
func (c *Client) SendAndWaitAck(ctx context.Context, req wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	if req.ChannelID == "" || req.ChannelType == 0 || req.SenderUID == "" || req.ClientMsgNo == "" {
		return wkprotoport.SendAck{}, fmt.Errorf("fake message sender: channel_id, channel_type, sender_uid, and client_msg_no are required")
	}
	id := atomic.AddInt64(&c.sender.next, 1)
	return wkprotoport.SendAck{MessageID: id, MessageSeq: uint64(id)}, nil
}
```

- [ ] **Step 6: Run fake sender tests to verify they pass**

Run:

```bash
GOWORK=off go test ./units/core/fake_message_sender
```

Expected: PASS.

- [ ] **Step 7: Register new units**

Modify imports in `cmd/wkbench/main.go`:

```go
fakemessagesender "github.com/WuKongIM/wkbench/units/core/fake_message_sender"
personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
sendtraffic "github.com/WuKongIM/wkbench/units/traffic/send"
```

Register them in `defaultRegistry`:

```go
fakemessagesender.Register(reg)
personpairs.Register(reg)
sendtraffic.Register(reg)
```

- [ ] **Step 8: Add real mixed WuKongIM example**

Create `examples/wukongim-send-rate-mixed.yaml`:

```yaml
version: wkbench/v2

run:
  id: wukongim-send-rate-mixed
  duration: 5s
  report_dir: ./reports/wukongim-send-rate-mixed

vars:
  users: 20
  groups: 2
  members: 10
  person_pairs: 5
  group_rate: 2/s
  person_rate: 5/s

units:
  target:
    use: wukongim.target
    spec:
      api_addrs: ["http://127.0.0.1:5001"]
      gateway_tcp_addrs: ["127.0.0.1:5100"]
      bench_api_token: ""
      operation_timeout: 5s

  identities:
    use: identity.pool
    spec:
      total: ${users}
      uid_prefix: bench-u
      device_prefix: bench-d
      token_prefix: bench-token

  tokens:
    use: wukongim.prepare_tokens

  groups:
    use: wukongim.prepare_group_channels
    spec:
      profile: mixed
      count: ${groups}
      members_per_channel: ${members}
      overlap: disallowed
      batch_size: 1000

  pairs:
    use: identity.person_pairs
    spec:
      count: ${person_pairs}
      mode: ring
      bidirectional: true

  sessions:
    use: wkproto.session_pool
    after: [tokens, groups]
    spec:
      connect_rate: 100/s

  group_traffic:
    use: traffic.send
    inputs:
      targets: groups.targets
      sender: sessions.message_sender
    spec:
      rate: ${group_rate}
      payload_size: 128
      sender_pick: round_robin
      max_in_flight: 8
      ack_timeout: 5s

  person_traffic:
    use: traffic.send
    inputs:
      targets: pairs.targets
      sender: sessions.message_sender
    spec:
      rate: ${person_rate}
      payload_size: 128
      sender_pick: round_robin
      max_in_flight: 8
      ack_timeout: 5s

  group_limits:
    use: report.assert
    inputs:
      summary: group_traffic.summary
    spec:
      rules:
        - metric: sendack_error_rate
          op: eq
          value: 0

  person_limits:
    use: report.assert
    inputs:
      summary: person_traffic.summary
    spec:
      rules:
        - metric: sendack_error_rate
          op: eq
          value: 0
```

- [ ] **Step 9: Run tests and scenario validation**

Run:

```bash
GOWORK=off go test ./cmd/wkbench ./units/core/fake_message_sender
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-send-rate-mixed.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-send-rate-mixed.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-send-rate-mixed.yaml
```

Expected: all commands PASS; validation prints `wkbench scenario is valid`, explain shows `group_traffic` and `person_traffic`, and plan shows both traffic units as completed.

- [ ] **Step 10: Commit**

```bash
git add cmd/wkbench/main.go cmd/wkbench/main_test.go examples/wukongim-send-rate-mixed.yaml units/core/fake_message_sender/unit.go units/core/fake_message_sender/unit_test.go
git commit -m "feat: register send rate workloads"
```

---

### Task 8: Documentation and Full Verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README unit list and commands**

In `README.md`, add these built-in units:

```markdown
- `core.fake_message_sender/v1`: produces a fake generic WKProto message sender for dry-run examples and tests.
- `identity.person_pairs/v1`: produces deterministic person-channel send targets.
- `traffic.send/v1`: sends protocol messages through `port.wkproto.message_sender/v1` and measures `SEND -> SENDACK` latency.
```

Add commands near the existing real WuKongIM example commands:

````markdown
Validate the mixed group/person send-rate scenario without connecting:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-send-rate-mixed.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-send-rate-mixed.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-send-rate-mixed.yaml
```
````

- [ ] **Step 2: Run focused tests**

Run:

```bash
GOWORK=off go test ./benchkit/contract ./benchkit/ports/channel ./benchkit/ports/wkproto ./units/wkproto/session_pool ./units/identity/person_pairs ./units/wukongim/prepare_group_channels ./units/traffic/send ./units/core/fake_message_sender ./cmd/wkbench
```

Expected: PASS.

- [ ] **Step 3: Run full wkbench tests**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run scenario checks**

Run:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-group-send.yaml
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-send-rate-mixed.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-send-rate-mixed.yaml -format json
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-send-rate-mixed.yaml -format json
```

Expected: all commands exit `0`. The JSON explain output contains `group_traffic` and `person_traffic`; the JSON plan output contains completed plans for both traffic units.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: describe send rate workloads"
```

---

## Final Review Checklist

- [ ] `traffic.group_send/v1` examples still validate.
- [ ] `traffic.send/v1` records nonzero send attempts and success/error counters.
- [ ] `traffic.send/v1` records one `sendack_latency` sample per successful sendack.
- [ ] Person sends use `ChannelType=1` and recipient uid as `ChannelID`.
- [ ] Group sends use `ChannelType=2` and prepared group channel ids.
- [ ] Mixed scenarios use independent rates for group and person workloads.
- [ ] `wkproto.session_pool/v1` still provides `group_sender` and also provides `message_sender`.
- [ ] `GOWORK=off go test ./...` passes in `wkbench`.
