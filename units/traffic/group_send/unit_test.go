package groupsend_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	groupsend "github.com/WuKongIM/wkbench/units/traffic/group_send"
)

func TestGroupSendUsesPortsAndEmitsSummary(t *testing.T) {
	unit := groupsend.Unit{}
	env := contract.NewTestRunEnv("run-1", "traffic", map[string]any{
		"channels": fakeGroupSet{},
		"sender":   &fakeGroupSender{},
	}, map[string]any{
		"rate":         "2/s",
		"payload_size": 32,
		"ack_timeout":  "1s",
	})
	env.SetRunDuration(2 * time.Second)

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
	if summary.SendackOK != 4 {
		t.Fatalf("expected four successful sendacks, got %#v", summary)
	}
	if got := env.CounterValue("send_attempt_total"); got != 4 {
		t.Fatalf("expected four attempts, got %v", got)
	}
	if got := env.CounterValue("sendack_success_total"); got != 4 {
		t.Fatalf("expected four successes, got %v", got)
	}
}

func TestGroupSendPlanReportsDeterministicShard(t *testing.T) {
	unit := groupsend.Unit{}
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

func TestGroupSendDeclaresDurationMetric(t *testing.T) {
	def := groupsend.Unit{}.Definition()
	for _, metric := range def.Metrics {
		if metric.Name == "sendack_latency" {
			if metric.Type != "duration" {
				t.Fatalf("sendack_latency metric type = %q, want duration", metric.Type)
			}
			return
		}
	}
	t.Fatal("sendack_latency metric is not declared")
}

type fakeGroupSet struct{}

func (fakeGroupSet) Count() int {
	return 2
}

func (fakeGroupSet) At(index int) channelport.GroupChannel {
	return channelport.GroupChannel{
		ChannelID: "g-" + string(rune('a'+index)),
		Members:   []string{"u1", "u2"},
	}
}

type fakeGroupSender struct {
	client fakeGroupClient
}

func (s *fakeGroupSender) Client(uid string) (wkprotoport.GroupClient, bool) {
	return &s.client, true
}

type fakeGroupClient struct{}

func (fakeGroupClient) SendGroupAndWaitAck(ctx context.Context, req wkprotoport.GroupSendRequest) (wkprotoport.SendAck, error) {
	if req.ChannelID == "" || req.SenderUID == "" || req.ClientMsgNo == "" || len(req.Payload) != 32 {
		return wkprotoport.SendAck{}, context.Canceled
	}
	return wkprotoport.SendAck{MessageID: 1, MessageSeq: 1}, nil
}
