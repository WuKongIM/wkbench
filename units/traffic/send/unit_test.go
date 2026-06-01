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
	sender := &messageSender{clients: map[string]wkprotoport.MessageClient{
		"u1": client,
		"u2": client,
	}}
	unit := sendunit.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "g1", ChannelType: 2, SenderUIDs: []string{"u1", "u2"}},
			{ChannelID: "p1", ChannelType: 1, SenderUIDs: []string{"u2"}},
		}},
		"sender": sender,
	}, map[string]any{
		"rate":          "2000/s",
		"payload_size":  16,
		"sender_pick":   "round_robin",
		"ack_timeout":   "1s",
		"max_in_flight": 1,
	})
	env.SetRunDuration(time.Millisecond)

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
		t.Fatalf("expected two attempts, got %v", got)
	}
	if got := env.CounterValue("sendack_success_total"); got != 2 {
		t.Fatalf("expected two successes, got %v", got)
	}
	if got := env.CounterValue("sendack_error_total"); got != 0 {
		t.Fatalf("expected no errors, got %v", got)
	}
	if samples := env.DurationValues("sendack_latency"); len(samples) != 2 {
		t.Fatalf("expected two latency samples, got %d", len(samples))
	}
	requests := client.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected two requests, got %d", len(requests))
	}
	if requests[0].ChannelID != "g1" || requests[0].ChannelType != 2 || requests[0].SenderUID != "u1" {
		t.Fatalf("unexpected first request: %#v", requests[0])
	}
	if requests[1].ChannelID != "p1" || requests[1].ChannelType != 1 || requests[1].SenderUID != "u2" {
		t.Fatalf("unexpected second request: %#v", requests[1])
	}
}

func TestSendRecordsErrorsAndContinues(t *testing.T) {
	client := &recordingClient{errOnCall: 2}
	unit := sendunit.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "g1", ChannelType: 2, SenderUIDs: []string{"u1"}},
		}},
		"sender": &messageSender{clients: map[string]wkprotoport.MessageClient{"u1": client}},
	}, map[string]any{
		"rate":         "2/s",
		"payload_size": 16,
	})
	env.SetRunDuration(time.Second)

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
	if summary.SendackOK != 1 || summary.SendackErrors != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if got := env.CounterValue("sendack_success_total"); got != 1 {
		t.Fatalf("expected one success, got %v", got)
	}
	if got := env.CounterValue("sendack_error_total"); got != 1 {
		t.Fatalf("expected one error, got %v", got)
	}
}

func TestSendReturnsContextErrorFromInFlightSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &cancelingClient{cancel: cancel}
	unit := sendunit.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "g1", ChannelType: 2, SenderUIDs: []string{"u1"}},
		}},
		"sender": &messageSender{clients: map[string]wkprotoport.MessageClient{"u1": client}},
	}, map[string]any{
		"rate":         "1/s",
		"payload_size": 16,
	})
	env.SetRunDuration(time.Second)

	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	err := unit.Run(ctx, env)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	if got := env.CounterValue("sendack_error_total"); got != 0 {
		t.Fatalf("cancellation counted as data-plane error: %v", got)
	}
}

func TestSendTreatsAckDeadlineAsDataPlaneError(t *testing.T) {
	client := &deadlineClient{}
	unit := sendunit.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "g1", ChannelType: 2, SenderUIDs: []string{"u1"}},
		}},
		"sender": &messageSender{clients: map[string]wkprotoport.MessageClient{"u1": client}},
	}, map[string]any{
		"rate":         "2/s",
		"payload_size": 16,
		"ack_timeout":  "1ms",
	})
	env.SetRunDuration(time.Second)

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
	if summary.SendackOK != 1 || summary.SendackErrors != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if got := env.CounterValue("sendack_error_total"); got != 1 {
		t.Fatalf("expected one error, got %v", got)
	}
}

func TestSendPlanReportsDeterministicShard(t *testing.T) {
	unit := sendunit.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", nil, map[string]any{
		"rate":          "2.5/s",
		"payload_size":  32,
		"sender_pick":   "round_robin",
		"max_in_flight": 8,
	})
	env.SetRunDuration(2 * time.Second)

	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	plan, err := unit.Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.UnitName != "traffic" {
		t.Fatalf("unexpected unit name %q", plan.UnitName)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("unexpected shards: %#v", plan.Shards)
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
	unit := sendunit.Unit{}
	tests := []struct {
		name string
		spec map[string]any
	}{
		{name: "zero rate", spec: map[string]any{"rate": "0/s"}},
		{name: "bad payload", spec: map[string]any{"rate": "1/s", "payload_size": -1}},
		{name: "bad in-flight", spec: map[string]any{"rate": "1/s", "max_in_flight": -1}},
		{name: "bad sender pick", spec: map[string]any{"rate": "1/s", "sender_pick": "random"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := contract.NewTestRunEnv("run-1", "traffic", nil, tt.spec)
			if err := unit.Validate(context.Background(), env); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestScheduledStart(t *testing.T) {
	start := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	got := sendunit.ScheduledStartForTest(start, contract.Rate{PerSecond: 4}, 3)
	want := start.Add(750 * time.Millisecond)
	if !got.Equal(want) {
		t.Fatalf("scheduled start = %v, want %v", got, want)
	}
}

func TestSendHonorsMaxInFlight(t *testing.T) {
	client := &recordingClient{delay: 2 * time.Millisecond}
	unit := sendunit.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"targets": targetSet{items: []channelport.SendTarget{
			{ChannelID: "g1", ChannelType: 2, SenderUIDs: []string{"u1"}},
		}},
		"sender": &messageSender{clients: map[string]wkprotoport.MessageClient{"u1": client}},
	}, map[string]any{
		"rate":          "100000/s",
		"payload_size":  16,
		"max_in_flight": 2,
	})
	env.SetRunDuration(50 * time.Microsecond)

	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := client.MaxActive(); got > 2 {
		t.Fatalf("max active sends = %d, want <= 2", got)
	}
}

type targetSet struct {
	items []channelport.SendTarget
}

func (s targetSet) Count() int {
	return len(s.items)
}

func (s targetSet) At(index int) channelport.SendTarget {
	return s.items[index]
}

type messageSender struct {
	clients map[string]wkprotoport.MessageClient
}

func (s *messageSender) MessageClient(uid string) (wkprotoport.MessageClient, bool) {
	client, ok := s.clients[uid]
	return client, ok
}

type recordingClient struct {
	errOnCall int
	delay     time.Duration

	mu        sync.Mutex
	requests  []wkprotoport.SendRequest
	calls     int
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
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()

	if c.delay > 0 {
		timer := time.NewTimer(c.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return wkprotoport.SendAck{}, ctx.Err()
		case <-timer.C:
		}
	}
	if c.errOnCall == call {
		return wkprotoport.SendAck{}, errors.New("send failed")
	}
	if req.ChannelID == "" || req.SenderUID == "" || req.ClientMsgNo == "" || len(req.Payload) != 16 {
		return wkprotoport.SendAck{}, errors.New("bad request")
	}
	return wkprotoport.SendAck{MessageID: int64(call), MessageSeq: uint64(call)}, nil
}

func (c *recordingClient) Requests() []wkprotoport.SendRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]wkprotoport.SendRequest(nil), c.requests...)
}

func (c *recordingClient) MaxActive() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxActive
}

type cancelingClient struct {
	cancel context.CancelFunc
}

func (c *cancelingClient) SendAndWaitAck(ctx context.Context, req wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	c.cancel()
	<-ctx.Done()
	return wkprotoport.SendAck{}, ctx.Err()
}

type deadlineClient struct {
	calls int
}

func (c *deadlineClient) SendAndWaitAck(ctx context.Context, req wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	c.calls++
	if c.calls == 1 {
		return wkprotoport.SendAck{}, context.DeadlineExceeded
	}
	return wkprotoport.SendAck{MessageID: int64(c.calls), MessageSeq: uint64(c.calls)}, nil
}
