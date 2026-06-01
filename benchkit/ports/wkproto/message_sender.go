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
