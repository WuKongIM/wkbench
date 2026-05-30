package target_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wukongtarget "github.com/WuKongIM/wkbench/units/wukongim/target"
)

func TestTargetProducesBlackBoxEndpointPort(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "target", nil, map[string]any{
		"api_addrs":         []string{"http://127.0.0.1:5001"},
		"gateway_tcp_addrs": []string{"127.0.0.1:5100"},
		"bench_api_token":   "token",
		"operation_timeout": "3s",
		"skip_readiness":    true,
		"skip_capabilities": true,
	})

	unit := wukongtarget.Unit{}
	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	target, err := contract.Output[targetport.Target](env, "target")
	if err != nil {
		t.Fatalf("target output: %v", err)
	}
	if target.APIAddrs[0] != "http://127.0.0.1:5001" || target.GatewayTCPAddrs[0] != "127.0.0.1:5100" || target.BenchAPIToken != "token" {
		t.Fatalf("unexpected target: %#v", target)
	}
}
