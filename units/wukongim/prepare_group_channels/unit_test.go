package preparegroupchannels_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	preparegroups "github.com/WuKongIM/wkbench/units/wukongim/prepare_group_channels"
)

func TestPrepareGroupChannelsPostsChannelsSubscribersAndOutputsGroupSet(t *testing.T) {
	var channelsReq struct {
		Channels []struct {
			ChannelID   string `json:"channel_id"`
			ChannelType uint8  `json:"channel_type"`
		} `json:"channels"`
	}
	var subscribersReq struct {
		Items []struct {
			ChannelID   string   `json:"channel_id"`
			Subscribers []string `json:"subscribers"`
		} `json:"items"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels":
			if err := json.NewDecoder(r.Body).Decode(&channelsReq); err != nil {
				t.Fatalf("decode channels: %v", err)
			}
		case "/bench/v1/channels/subscribers":
			if err := json.NewDecoder(r.Body).Decode(&subscribersReq); err != nil {
				t.Fatalf("decode subscribers: %v", err)
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	env := contract.NewTestRunEnv("run-1", "groups", map[string]any{
		"target": targetport.Target{APIAddrs: []string{server.URL}},
		"identities": identityport.PoolData{Items: []identityport.Identity{
			{UID: "u1"}, {UID: "u2"}, {UID: "u3"}, {UID: "u4"},
		}},
	}, map[string]any{
		"profile":             "small",
		"count":               2,
		"members_per_channel": 2,
		"overlap":             "disallowed",
		"batch_size":          1000,
	})

	if err := (preparegroups.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(channelsReq.Channels) != 2 || channelsReq.Channels[0].ChannelID != "run-1-small-0" {
		t.Fatalf("unexpected channels request: %#v", channelsReq)
	}
	if len(subscribersReq.Items) != 2 || subscribersReq.Items[1].Subscribers[1] != "u4" {
		t.Fatalf("unexpected subscribers request: %#v", subscribersReq)
	}
	groups, err := contract.Output[channelport.GroupSet](env, "channels")
	if err != nil {
		t.Fatalf("channels output: %v", err)
	}
	if groups.Count() != 2 || groups.At(1).Members[1] != "u4" {
		t.Fatalf("unexpected groups output: count=%d second=%#v", groups.Count(), groups.At(1))
	}
	targets, err := contract.Output[channelport.SendTargetSet](env, "targets")
	if err != nil {
		t.Fatalf("targets output: %v", err)
	}
	if targets.Count() != 2 {
		t.Fatalf("unexpected target count: %d", targets.Count())
	}
	firstTarget := targets.At(0)
	if firstTarget.ChannelID != "run-1-small-0" || firstTarget.ChannelType != 2 || len(firstTarget.SenderUIDs) != 2 || firstTarget.SenderUIDs[0] != "u1" {
		t.Fatalf("unexpected first target: %#v", firstTarget)
	}
}
