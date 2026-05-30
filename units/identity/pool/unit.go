// Package pool implements identity.pool/v1.
package pool

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const kind = "identity.pool/v1"

// Unit produces deterministic benchmark identities.
type Unit struct{}

// Spec configures identity generation.
type Spec struct {
	// Total is the number of generated identities.
	Total int `json:"total" yaml:"total"`
	// UIDPrefix is prepended to generated user ids.
	UIDPrefix string `json:"uid_prefix" yaml:"uid_prefix"`
	// DevicePrefix is prepended to generated device ids.
	DevicePrefix string `json:"device_prefix" yaml:"device_prefix"`
	// TokenPrefix is prepended to generated tokens when non-empty.
	TokenPrefix string `json:"token_prefix" yaml:"token_prefix"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Identity pool",
		Description: "Produces deterministic benchmark user and device identities.",
		Outputs: []contract.PortDef{
			{Name: "pool", Type: identityport.PoolV1},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	if spec.Total <= 0 {
		return fmt.Errorf("total must be greater than zero")
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
	items := make([]identityport.Identity, 0, spec.Total)
	for i := 0; i < spec.Total; i++ {
		identity := identityport.Identity{
			UID:      fmt.Sprintf("%s-%d", spec.UIDPrefix, i),
			DeviceID: fmt.Sprintf("%s-%d", spec.DevicePrefix, i),
		}
		if spec.TokenPrefix != "" {
			identity.Token = fmt.Sprintf("%s-%d", spec.TokenPrefix, i)
		}
		items = append(items, identity)
	}
	return env.SetOutput("pool", Pool{Items: items})
}

func decodeSpec(env contract.ValidateEnv) (Spec, error) {
	spec := Spec{UIDPrefix: "user", DevicePrefix: "device"}
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	spec.UIDPrefix = strings.TrimSpace(spec.UIDPrefix)
	if spec.UIDPrefix == "" {
		spec.UIDPrefix = "user"
	}
	spec.DevicePrefix = strings.TrimSpace(spec.DevicePrefix)
	if spec.DevicePrefix == "" {
		spec.DevicePrefix = "device"
	}
	spec.TokenPrefix = strings.TrimSpace(spec.TokenPrefix)
	return spec, nil
}

// Pool is a JSON-friendly identity pool.
type Pool struct {
	// Items contains deterministic identities.
	Items []identityport.Identity `json:"items"`
}

// Count implements identity.Pool.
func (p Pool) Count() int { return len(p.Items) }

// At implements identity.Pool.
func (p Pool) At(index int) identityport.Identity { return p.Items[index] }

// TokenFor implements identity.TokenSource.
func (p Pool) TokenFor(uid string) (string, bool) {
	for _, item := range p.Items {
		if item.UID == uid {
			return item.Token, item.Token != ""
		}
	}
	return "", false
}
