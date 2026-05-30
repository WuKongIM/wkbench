// Package fakegroupsender provides a deterministic in-memory group sender.
package fakegroupsender

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const kind = "core.fake_group_sender/v1"

// Unit produces a fake group sender for dry-run examples.
type Unit struct{}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Fake group sender",
		Description: "Produces a deterministic in-memory sender that accepts every group send.",
		Outputs: []contract.PortDef{
			{Name: "sender", Type: wkprotoport.GroupSenderV1},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

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

// Client implements wkproto.GroupSender.
func (s *Sender) Client(uid string) (wkprotoport.GroupClient, bool) {
	if uid == "" {
		return nil, false
	}
	return &Client{sender: s}, true
}

// Client acknowledges every send with a monotonically increasing message id.
type Client struct {
	sender *Sender
}

// SendGroupAndWaitAck implements wkproto.GroupClient.
func (c *Client) SendGroupAndWaitAck(ctx context.Context, req wkprotoport.GroupSendRequest) (wkprotoport.SendAck, error) {
	if req.ChannelID == "" || req.SenderUID == "" || req.ClientMsgNo == "" {
		return wkprotoport.SendAck{}, fmt.Errorf("fake group sender: channel_id, sender_uid, and client_msg_no are required")
	}
	id := atomic.AddInt64(&c.sender.next, 1)
	return wkprotoport.SendAck{MessageID: id, MessageSeq: uint64(id)}, nil
}
