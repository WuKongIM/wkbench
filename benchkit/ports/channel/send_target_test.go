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
