// Package channel defines public channel-related ports.
package channel

import "github.com/WuKongIM/wkbench/benchkit/contract"

// GroupSetV1 is the port type for deterministic group channel sets.
const GroupSetV1 contract.PortType = "port.channel.group_set/v1"

// GroupSet exposes generated or discovered group channels.
type GroupSet interface {
	// Count returns the number of group channels.
	Count() int
	// At returns the group channel at index.
	At(index int) GroupChannel
}

// GroupChannel describes one group channel and its usable members.
type GroupChannel struct {
	// ChannelID is the protocol channel id.
	ChannelID string `json:"channel_id"`
	// Members are user ids that can participate in the group.
	Members []string `json:"members"`
}
