// Package send implements traffic.send/v1.
package send

import (
	"context"
	"errors"
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

// Unit sends protocol messages over generic send targets.
type Unit struct{}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Spec configures traffic.send/v1.
type Spec struct {
	// Rate is the total offered send rate.
	Rate contract.Rate `json:"rate" yaml:"rate"`
	// PayloadSize is the deterministic payload size in bytes.
	PayloadSize int `json:"payload_size" yaml:"payload_size"`
	// SenderPick selects which target sender sends each message.
	SenderPick string `json:"sender_pick" yaml:"sender_pick"`
	// MaxInFlight bounds concurrent sendack waits.
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

type sendResult struct {
	ack     wkprotoport.SendAck
	latency time.Duration
	err     error
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "SEND traffic",
		Description: "Sends messages using public send target and WKProto ports.",
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

	results := make(chan sendResult, maxInFlight)
	start := time.Now()
	var summary trafficport.Summary
	var launched, completed int64
	inFlight := 0

	for completed < messageCount {
		if launched < messageCount && inFlight < maxInFlight {
			due := scheduledStart(start, spec.Rate, launched)
			if wait := time.Until(due); wait > 0 {
				if inFlight == 0 {
					if err := waitForSchedule(ctx, wait); err != nil {
						return err
					}
					continue
				}
				select {
				case result := <-results:
					if err := terminalContextError(ctx, result.err); err != nil {
						return err
					}
					applyResult(env, &summary, result)
					completed++
					inFlight--
					continue
				case <-time.After(wait):
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			msgIndex := launched
			launched++
			inFlight++
			env.EmitCounter("send_attempt_total", 1, nil)
			go func() {
				result := sendOne(ctx, env, spec, ackTimeout, targets, sender, msgIndex)
				select {
				case results <- result:
				case <-ctx.Done():
				}
			}()
			continue
		}

		select {
		case result := <-results:
			if err := terminalContextError(ctx, result.err); err != nil {
				return err
			}
			applyResult(env, &summary, result)
			completed++
			inFlight--
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	summary.ElapsedMS = elapsedMilliseconds(start)
	return env.SetOutput("summary", summary)
}

func terminalContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return nil
	}
	if errors.Is(err, ctxErr) {
		return ctxErr
	}
	return nil
}

func waitForSchedule(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func applyResult(env contract.RunEnv, summary *trafficport.Summary, result sendResult) {
	if result.err != nil {
		env.EmitCounter("sendack_error_total", 1, nil)
		summary.SendackErrors++
		return
	}
	env.EmitCounter("sendack_success_total", 1, nil)
	env.ObserveDuration("sendack_latency", sendackLatency(result), nil)
	summary.SendackOK++
	summary.LastMessageID = result.ack.MessageID
}

func sendackLatency(result sendResult) time.Duration {
	if result.ack.WireLatency > 0 {
		return result.ack.WireLatency
	}
	return result.latency
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

func sendOne(ctx context.Context, env contract.RunEnv, spec Spec, ackTimeout time.Duration, targets channelport.SendTargetSet, sender wkprotoport.MessageSender, msgIndex int64) sendResult {
	target := targets.At(int(msgIndex % int64(targets.Count())))
	if len(target.SenderUIDs) == 0 {
		return sendResult{err: fmt.Errorf("send: target %q has no online senders", target.ChannelID)}
	}
	senderUID := pickSender(target, spec.SenderPick, msgIndex)
	client, ok := sender.MessageClient(senderUID)
	if !ok {
		return sendResult{err: fmt.Errorf("send: missing sender client %q", senderUID)}
	}
	req := wkprotoport.SendRequest{
		ChannelID:   target.ChannelID,
		ChannelType: target.ChannelType,
		SenderUID:   senderUID,
		ClientMsgNo: env.NextID("msg"),
		Payload:     env.Payload(spec.PayloadSize),
		Timeout:     ackTimeout,
	}
	latencyStart := time.Now()
	ack, err := client.SendAndWaitAck(ctx, req)
	if err != nil {
		return sendResult{err: err}
	}
	return sendResult{ack: ack, latency: time.Since(latencyStart)}
}

func pickSender(target channelport.SendTarget, mode string, msgIndex int64) string {
	if mode == senderPickRoundRobin {
		return target.SenderUIDs[int(msgIndex%int64(len(target.SenderUIDs)))]
	}
	return target.SenderUIDs[0]
}

func scheduledStart(start time.Time, rate contract.Rate, msgIndex int64) time.Time {
	if rate.PerSecond <= 0 {
		return start
	}
	return start.Add(time.Duration(float64(time.Second) * float64(msgIndex) / rate.PerSecond))
}

// ScheduledStartForTest exposes deterministic pacing math to tests.
func ScheduledStartForTest(start time.Time, rate contract.Rate, msgIndex int64) time.Time {
	return scheduledStart(start, rate, msgIndex)
}
