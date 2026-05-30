package staticgroups_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
)

func TestStaticGroupsProducesGroupSetPort(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "groups", nil, map[string]any{
		"count":               2,
		"members_per_channel": 3,
		"channel_prefix":      "g",
		"uid_prefix":          "u",
	})

	unit := staticgroups.Unit{}
	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	groups, err := contract.Output[channelport.GroupSet](env, "groups")
	if err != nil {
		t.Fatalf("groups output: %v", err)
	}
	if groups.Count() != 2 {
		t.Fatalf("expected 2 groups, got %d", groups.Count())
	}
	first := groups.At(0)
	if first.ChannelID != "g-0" || len(first.Members) != 3 || first.Members[2] != "u-2" {
		t.Fatalf("unexpected first group: %#v", first)
	}
}
