// Package sessionpool implements wkproto.session_pool/v1.
package sessionpool

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const kind = "wkproto.session_pool/v1"

// Client is the session client retained by the pool.
type Client interface {
	wkprotoport.GroupClient
	// Close releases the underlying session.
	Close() error
}

// ClientFactory creates one connected session client.
type ClientFactory func(ctx context.Context, target targetport.Target, identity identityport.Identity, token string, gatewayAddr string) (Client, error)

// Unit opens WKProto sessions and provides group sender access.
type Unit struct {
	// ClientFactory overrides production session creation in tests.
	ClientFactory ClientFactory
}

// Spec configures WKProto session creation.
type Spec struct {
	// ConnectRate reserves a rate limit shape for later distributed runners.
	ConnectRate contract.Rate `json:"connect_rate" yaml:"connect_rate"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "WKProto session pool",
		Description: "Opens WKProto sessions and exposes group sending clients by uid.",
		Inputs: []contract.PortDef{
			{Name: "target", Type: targetport.TargetV1},
			{Name: "identities", Type: identityport.PoolV1},
			{Name: "tokens", Type: identityport.TokenSourceV1, Optional: true},
		},
		Outputs: []contract.PortDef{
			{Name: "group_sender", Type: wkprotoport.GroupSenderV1},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return err
	}
	if spec.ConnectRate.PerSecond < 0 {
		return fmt.Errorf("connect_rate must not be negative")
	}
	return nil
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

// Run implements contract.Unit.
func (u Unit) Run(ctx context.Context, env contract.RunEnv) error {
	tgt, err := contract.Input[targetport.Target](env, "target")
	if err != nil {
		return err
	}
	if len(tgt.GatewayTCPAddrs) == 0 {
		return fmt.Errorf("session_pool: target has no gateway_tcp_addrs")
	}
	identities, err := contract.Input[identityport.Pool](env, "identities")
	if err != nil {
		return err
	}
	var tokens identityport.TokenSource
	if value, err := env.Input("tokens"); err == nil {
		var ok bool
		tokens, ok = value.(identityport.TokenSource)
		if !ok {
			return fmt.Errorf("session_pool: tokens input has unexpected type %T", value)
		}
	}
	factory := u.ClientFactory
	if factory == nil {
		factory = NewProductionClient
	}
	pool := &Pool{clients: make(map[string]Client, identities.Count())}
	for i := 0; i < identities.Count(); i++ {
		identity := identities.At(i)
		token := identity.Token
		if tokens != nil {
			if resolved, ok := tokens.TokenFor(identity.UID); ok {
				token = resolved
			}
		}
		gateway := strings.TrimSpace(tgt.GatewayTCPAddrs[i%len(tgt.GatewayTCPAddrs)])
		client, err := factory(ctx, tgt, identity, token, gateway)
		if err != nil {
			_ = pool.Close()
			return err
		}
		pool.clients[identity.UID] = client
	}
	return env.SetOutput("group_sender", pool)
}

// Pool stores connected clients by uid.
type Pool struct {
	clients map[string]Client
}

// Client implements wkproto.GroupSender.
func (p *Pool) Client(uid string) (wkprotoport.GroupClient, bool) {
	client, ok := p.clients[uid]
	return client, ok
}

// Close releases every client.
func (p *Pool) Close() error {
	var first error
	for _, client := range p.clients {
		if err := client.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
