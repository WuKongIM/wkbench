// Package preparetokens implements wukongim.prepare_tokens/v1.
package preparetokens

import (
	"context"
	"fmt"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	"github.com/WuKongIM/wkbench/benchkit/registry"
	"github.com/WuKongIM/wkbench/units/wukongim/internal/benchapi"
)

const kind = "wukongim.prepare_tokens/v1"

// Unit prepares benchmark user tokens through /bench/v1/users/tokens.
type Unit struct{}

// Spec configures token preparation.
type Spec struct {
	// BatchSize is reserved for chunking; zero uses one batch.
	BatchSize int `json:"batch_size" yaml:"batch_size"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "WuKongIM prepare tokens",
		Description: "Prepares benchmark user tokens through the target bench API.",
		Inputs: []contract.PortDef{
			{Name: "target", Type: targetport.TargetV1},
			{Name: "identities", Type: identityport.PoolV1},
		},
		Outputs: []contract.PortDef{
			{Name: "tokens", Type: identityport.TokenSourceV1},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return err
	}
	if spec.BatchSize < 0 {
		return fmt.Errorf("batch_size must not be negative")
	}
	return nil
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	tgt, err := contract.Input[targetport.Target](env, "target")
	if err != nil {
		return err
	}
	pool, err := contract.Input[identityport.Pool](env, "identities")
	if err != nil {
		return err
	}
	items := make([]benchapi.UserTokenItem, 0, pool.Count())
	source := Tokens{Tokens: make(map[string]string, pool.Count())}
	for i := 0; i < pool.Count(); i++ {
		identity := pool.At(i)
		items = append(items, benchapi.UserTokenItem{UID: identity.UID, Token: identity.Token})
		source.Tokens[identity.UID] = identity.Token
	}
	client := benchapi.NewClient(benchapi.Config{APIAddrs: tgt.APIAddrs, Token: tgt.BenchAPIToken})
	if err := client.UpsertTokens(ctx, benchapi.BatchTokensRequest{
		RunID:   env.RunID(),
		BatchID: env.UnitName() + "-0",
		Upsert:  true,
		Users:   items,
	}); err != nil {
		return err
	}
	return env.SetOutput("tokens", source)
}

// Tokens is a JSON-friendly prepared token source.
type Tokens struct {
	// Tokens maps uid to prepared token.
	Tokens map[string]string `json:"tokens"`
}

// TokenFor implements identity.TokenSource.
func (t Tokens) TokenFor(uid string) (string, bool) {
	token, ok := t.Tokens[uid]
	return token, ok
}
