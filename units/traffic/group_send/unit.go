// Package groupsend implements traffic.group_send/v1.
package groupsend

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
)

const (
	kind                  = "traffic.group_send/v1"
	defaultAckTimeout     = 5 * time.Second
	senderPickFirstOnline = "first_online"
	senderPickRoundRobin  = "round_robin"
)

// Unit sends group messages over a connected group sender port.
type Unit struct{}

// Spec configures traffic.group_send/v1.
type Spec struct {
	// Rate is the total offered send rate.
	Rate contract.Rate `json:"rate" yaml:"rate"`
	// PayloadSize is the deterministic payload size in bytes.
	PayloadSize int `json:"payload_size" yaml:"payload_size"`
	// SenderPick selects which group member sends each message.
	SenderPick string `json:"sender_pick" yaml:"sender_pick"`
	// MaxInFlight reserves the concurrency shape for later distributed runners.
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

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Group SEND traffic",
		Description: "Sends group messages using public channel and WKProto ports.",
		Inputs: []contract.PortDef{
			{Name: "channels", Type: channelport.GroupSetV1},
			{Name: "sender", Type: wkprotoport.GroupSenderV1},
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
		Shards: []any{
			planShard{
				TotalMessages: totalMessages(spec.Rate, env.RunDuration()),
				RatePerSecond: spec.Rate.PerSecond,
				DurationMS:    env.RunDuration().Milliseconds(),
				PayloadSize:   spec.PayloadSize,
				SenderPick:    spec.SenderPick,
				MaxInFlight:   spec.MaxInFlight,
			},
		},
	}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	channels, err := contract.Input[channelport.GroupSet](env, "channels")
	if err != nil {
		return err
	}
	sender, err := contract.Input[wkprotoport.GroupSender](env, "sender")
	if err != nil {
		return err
	}
	if channels.Count() <= 0 {
		return fmt.Errorf("group_send: channels input is empty")
	}
	messageCount := totalMessages(spec.Rate, env.RunDuration())
	ackTimeout := spec.AckTimeout.Duration
	if ackTimeout <= 0 {
		ackTimeout = defaultAckTimeout
	}
	var summary trafficport.Summary
	start := time.Now()
	for idx := int64(0); idx < messageCount; idx++ {
		env.EmitCounter("send_attempt_total", 1, nil)
		ack, err := sendOne(ctx, env, spec, ackTimeout, channels, sender, idx)
		if err != nil {
			env.EmitCounter("sendack_error_total", 1, nil)
			summary.SendackErrors++
			continue
		}
		env.EmitCounter("sendack_success_total", 1, nil)
		env.ObserveDuration("sendack_latency", ack.WireLatency, nil)
		summary.SendackOK++
		summary.LastMessageID = ack.MessageID
	}
	summary.ElapsedMS = elapsedMilliseconds(start)
	return env.SetOutput("summary", summary)
}

func elapsedMilliseconds(start time.Time) int64 {
	elapsed := time.Since(start).Milliseconds()
	if elapsed < 1 {
		return 1
	}
	return elapsed
}

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

func sendOne(ctx context.Context, env contract.RunEnv, spec Spec, ackTimeout time.Duration, channels channelport.GroupSet, sender wkprotoport.GroupSender, msgIndex int64) (wkprotoport.SendAck, error) {
	channel := channels.At(int(msgIndex % int64(channels.Count())))
	if len(channel.Members) == 0 {
		return wkprotoport.SendAck{}, fmt.Errorf("group_send: channel %q has no online members", channel.ChannelID)
	}
	senderUID := pickSender(channel, spec.SenderPick, msgIndex)
	client, ok := sender.Client(senderUID)
	if !ok {
		return wkprotoport.SendAck{}, fmt.Errorf("group_send: missing sender client %q", senderUID)
	}
	return client.SendGroupAndWaitAck(ctx, wkprotoport.GroupSendRequest{
		ChannelID:   channel.ChannelID,
		SenderUID:   senderUID,
		ClientMsgNo: env.NextID("msg"),
		Payload:     env.Payload(spec.PayloadSize),
		Timeout:     ackTimeout,
	})
}

func pickSender(channel channelport.GroupChannel, mode string, msgIndex int64) string {
	if mode == senderPickRoundRobin {
		return channel.Members[int(msgIndex%int64(len(channel.Members)))]
	}
	return channel.Members[0]
}
