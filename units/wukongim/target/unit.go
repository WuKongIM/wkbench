// Package target implements wukongim.target/v1.
package target

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	"github.com/WuKongIM/wkbench/benchkit/registry"
	"github.com/WuKongIM/wkbench/units/wukongim/internal/benchapi"
)

const kind = "wukongim.target/v1"

// Unit produces a black-box WuKongIM target endpoint.
type Unit struct{}

// Spec configures WuKongIM target endpoints.
type Spec struct {
	// APIAddrs are HTTP API base addresses.
	APIAddrs []string `json:"api_addrs" yaml:"api_addrs"`
	// GatewayTCPAddrs are WKProto TCP gateway addresses.
	GatewayTCPAddrs []string `json:"gateway_tcp_addrs" yaml:"gateway_tcp_addrs"`
	// BenchAPIToken is the optional bearer token for /bench/v1 routes.
	BenchAPIToken string `json:"bench_api_token" yaml:"bench_api_token"`
	// OperationTimeout bounds target operations.
	OperationTimeout contract.Duration `json:"operation_timeout" yaml:"operation_timeout"`
	// SkipReadiness skips /healthz and /readyz checks.
	SkipReadiness bool `json:"skip_readiness" yaml:"skip_readiness"`
	// SkipCapabilities skips /bench/v1/capabilities checks.
	SkipCapabilities bool `json:"skip_capabilities" yaml:"skip_capabilities"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "WuKongIM target",
		Description: "Produces black-box WuKongIM HTTP and WKProto endpoints.",
		Outputs: []contract.PortDef{
			{Name: "target", Type: targetport.TargetV1},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	if len(spec.APIAddrs) == 0 {
		return fmt.Errorf("api_addrs is required")
	}
	if len(spec.GatewayTCPAddrs) == 0 {
		return fmt.Errorf("gateway_tcp_addrs is required")
	}
	return nil
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	client := benchapi.NewClient(benchapi.Config{APIAddrs: spec.APIAddrs, Token: spec.BenchAPIToken})
	if !spec.SkipReadiness {
		if err := client.Healthz(ctx); err != nil {
			return err
		}
		if err := client.Readyz(ctx); err != nil {
			return err
		}
	}
	if !spec.SkipCapabilities {
		caps, err := client.Capabilities(ctx)
		if err != nil {
			return err
		}
		if !caps.Enabled {
			return fmt.Errorf("bench api capabilities disabled")
		}
	}
	return env.SetOutput("target", targetport.Target{
		APIAddrs:         spec.APIAddrs,
		GatewayTCPAddrs:  spec.GatewayTCPAddrs,
		BenchAPIToken:    spec.BenchAPIToken,
		OperationTimeout: spec.OperationTimeout.Duration,
	})
}

func decodeSpec(env contract.ValidateEnv) (Spec, error) {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	spec.APIAddrs = nonEmptyStrings(spec.APIAddrs)
	spec.GatewayTCPAddrs = nonEmptyStrings(spec.GatewayTCPAddrs)
	spec.BenchAPIToken = strings.TrimSpace(spec.BenchAPIToken)
	return spec, nil
}

func nonEmptyStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
