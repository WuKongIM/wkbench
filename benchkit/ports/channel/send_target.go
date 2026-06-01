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
