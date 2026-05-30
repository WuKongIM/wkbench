// Package wkproto defines WKProto capability ports.
package wkproto

import (
	"context"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

// GroupSenderV1 is the port type for sending group messages.
const GroupSenderV1 contract.PortType = "port.wkproto.group_sender/v1"

// GroupSender returns group-capable clients by uid.
type GroupSender interface {
	// Client returns the connected client for uid.
	Client(uid string) (GroupClient, bool)
}

// GroupClient sends one group message and waits for sendack.
type GroupClient interface {
	// SendGroupAndWaitAck sends req and waits for the matching sendack.
	SendGroupAndWaitAck(ctx context.Context, req GroupSendRequest) (SendAck, error)
}

// GroupSendRequest describes one group send operation.
type GroupSendRequest struct {
	// ChannelID is the target group channel id.
	ChannelID string
	// SenderUID is the connected sender user id.
	SenderUID string
	// ClientMsgNo is the deterministic client message number.
	ClientMsgNo string
	// Payload is the message payload.
	Payload []byte
	// Timeout bounds waiting for sendack.
	Timeout time.Duration
}

// SendAck captures the protocol send acknowledgment.
type SendAck struct {
	// MessageID is the acknowledged server message id.
	MessageID int64
	// MessageSeq is the acknowledged channel sequence.
	MessageSeq uint64
}
