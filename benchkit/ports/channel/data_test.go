package channel_test

import (
	"encoding/json"
	"testing"

	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
)

func TestGroupSetDataJSONRoundTripImplementsGroupSet(t *testing.T) {
	source := channelport.GroupSetData{Items: []channelport.GroupChannel{{
		ChannelID: "g1",
		Members:   []string{"u1"},
	}}}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded channelport.GroupSetData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var set channelport.GroupSet = decoded
	if set.Count() != 1 || set.At(0).ChannelID != "g1" {
		t.Fatalf("decoded set = %#v", decoded)
	}
}

func TestSendTargetSetDataJSONRoundTripImplementsSendTargetSet(t *testing.T) {
	source := channelport.SendTargetSetData{Items: []channelport.SendTarget{{
		ChannelID:   "u2",
		ChannelType: 1,
		SenderUIDs:  []string{"u1"},
	}}}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded channelport.SendTargetSetData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var set channelport.SendTargetSet = decoded
	if set.Count() != 1 || set.At(0).ChannelID != "u2" || set.At(0).SenderUIDs[0] != "u1" {
		t.Fatalf("decoded set = %#v", decoded)
	}
}
