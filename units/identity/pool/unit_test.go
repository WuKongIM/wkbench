package pool_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	identitypool "github.com/WuKongIM/wkbench/units/identity/pool"
)

func TestIdentityPoolProducesDeterministicIdentities(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "identities", nil, map[string]any{
		"total":         3,
		"uid_prefix":    "bench-u",
		"device_prefix": "bench-d",
		"token_prefix":  "bench-token",
	})

	unit := identitypool.Unit{}
	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	pool, err := contract.Output[identityport.Pool](env, "pool")
	if err != nil {
		t.Fatalf("pool output: %v", err)
	}
	if pool.Count() != 3 {
		t.Fatalf("expected 3 identities, got %d", pool.Count())
	}
	last := pool.At(2)
	if last.UID != "bench-u-2" || last.DeviceID != "bench-d-2" || last.Token != "bench-token-2" {
		t.Fatalf("unexpected identity: %#v", last)
	}
}
