package personpairs_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
	personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, personpairs.Unit{})
}

func TestPersonPairsGenerateRingTargets(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "pairs", map[string]any{
		"identities": identityPool{items: []identityport.Identity{{UID: "u1"}, {UID: "u2"}, {UID: "u3"}}},
	}, map[string]any{
		"count": 2,
		"mode":  "ring",
	})

	if err := (personpairs.Unit{}).Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := (personpairs.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	targets, err := contract.Output[channelport.SendTargetSet](env, "targets")
	if err != nil {
		t.Fatalf("targets output: %v", err)
	}
	if targets.Count() != 2 {
		t.Fatalf("target count = %d, want 2", targets.Count())
	}
	first := targets.At(0)
	second := targets.At(1)
	if first.ChannelID != "u2" || first.ChannelType != 1 || first.SenderUIDs[0] != "u1" {
		t.Fatalf("unexpected first target: %#v", first)
	}
	if second.ChannelID != "u3" || second.ChannelType != 1 || second.SenderUIDs[0] != "u2" {
		t.Fatalf("unexpected second target: %#v", second)
	}
}

func TestPersonPairsGenerateRingTargetsWraparound(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "pairs", map[string]any{
		"identities": identityPool{items: []identityport.Identity{{UID: "u1"}, {UID: "u2"}, {UID: "u3"}}},
	}, map[string]any{
		"count": 5,
		"mode":  "ring",
	})

	if err := (personpairs.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	targets, err := contract.Output[channelport.SendTargetSet](env, "targets")
	if err != nil {
		t.Fatalf("targets output: %v", err)
	}
	want := []struct {
		sender    string
		recipient string
	}{
		{sender: "u1", recipient: "u2"},
		{sender: "u2", recipient: "u3"},
		{sender: "u3", recipient: "u1"},
		{sender: "u1", recipient: "u2"},
		{sender: "u2", recipient: "u3"},
	}
	if targets.Count() != len(want) {
		t.Fatalf("target count = %d, want %d", targets.Count(), len(want))
	}
	for i, pair := range want {
		target := targets.At(i)
		if target.ChannelID != pair.recipient || target.ChannelType != 1 || target.SenderUIDs[0] != pair.sender {
			t.Fatalf("target %d = %#v, want sender %q recipient %q channel type 1", i, target, pair.sender, pair.recipient)
		}
	}
}

func TestPersonPairsBidirectionalExpandsEachPair(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "pairs", map[string]any{
		"identities": identityPool{items: []identityport.Identity{{UID: "u1"}, {UID: "u2"}}},
	}, map[string]any{
		"count":         1,
		"mode":          "ring",
		"bidirectional": true,
	})

	if err := (personpairs.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	targets, err := contract.Output[channelport.SendTargetSet](env, "targets")
	if err != nil {
		t.Fatalf("targets output: %v", err)
	}
	if targets.Count() != 2 {
		t.Fatalf("target count = %d, want 2", targets.Count())
	}
	if targets.At(0).ChannelID != "u2" || targets.At(0).SenderUIDs[0] != "u1" {
		t.Fatalf("unexpected forward target: %#v", targets.At(0))
	}
	if targets.At(1).ChannelID != "u1" || targets.At(1).SenderUIDs[0] != "u2" {
		t.Fatalf("unexpected reverse target: %#v", targets.At(1))
	}
}

func TestPersonPairsValidateRejectsInvalidSpec(t *testing.T) {
	for name, spec := range map[string]map[string]any{
		"missing count": {"mode": "ring"},
		"unknown mode":  {"count": 1, "mode": "random"},
	} {
		t.Run(name, func(t *testing.T) {
			env := contract.NewTestRunEnv("run-1", "pairs", nil, spec)
			if err := (personpairs.Unit{}).Validate(context.Background(), env); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestPersonPairsRunRejectsTooFewIdentities(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "pairs", map[string]any{
		"identities": identityPool{items: []identityport.Identity{{UID: "u1"}}},
	}, map[string]any{"count": 1, "mode": "ring"})

	if err := (personpairs.Unit{}).Run(context.Background(), env); err == nil {
		t.Fatal("expected run error")
	}
}

type identityPool struct {
	items []identityport.Identity
}

func (p identityPool) Count() int                         { return len(p.items) }
func (p identityPool) At(index int) identityport.Identity { return p.items[index] }
