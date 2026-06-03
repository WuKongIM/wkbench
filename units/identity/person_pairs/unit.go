// Package personpairs implements identity.person_pairs/v1.
package personpairs

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const (
	kind              = "identity.person_pairs/v1"
	modeRing          = "ring"
	personChannelType = uint8(1)
)

// Unit produces deterministic person-channel send targets.
type Unit struct{}

// Spec configures deterministic person pair generation.
type Spec struct {
	// Count is the number of base person pairs to produce.
	Count int `json:"count" yaml:"count"`
	// Mode selects the deterministic pairing algorithm.
	Mode string `json:"mode" yaml:"mode"`
	// Bidirectional also emits a reverse target for every base pair.
	Bidirectional bool `json:"bidirectional" yaml:"bidirectional"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Person send pairs",
		Description: "Produces deterministic person-channel send targets from an identity pool.",
		Inputs: []contract.PortDef{
			{Name: "identities", Type: identityport.PoolV1, Meta: inlineJSONDataMeta()},
		},
		Outputs: []contract.PortDef{
			{Name: "targets", Type: channelport.SendTargetSetV1, Meta: inlineJSONDataMeta()},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	if spec.Count <= 0 {
		return fmt.Errorf("count must be greater than zero")
	}
	if spec.Mode != modeRing {
		return fmt.Errorf("mode must be %q", modeRing)
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
	pool, err := contract.Input[identityport.PoolData](env, "identities")
	if err != nil {
		return err
	}
	if pool.Count() < 2 {
		return fmt.Errorf("identities must contain at least two identities")
	}

	items := make([]channelport.SendTarget, 0, spec.Count)
	if spec.Bidirectional {
		items = make([]channelport.SendTarget, 0, spec.Count*2)
	}
	for i := 0; i < spec.Count; i++ {
		senderUID := pool.At(i % pool.Count()).UID
		recipientUID := pool.At((i + 1) % pool.Count()).UID
		items = append(items, personTarget(senderUID, recipientUID))
		if spec.Bidirectional {
			items = append(items, personTarget(recipientUID, senderUID))
		}
	}
	return env.SetOutput("targets", TargetSet{Items: items})
}

func inlineJSONDataMeta() contract.PortMeta {
	return contract.PortMeta{
		Boundary:        contract.PortBoundaryData,
		Transport:       contract.PortTransportInline,
		Encodings:       []string{"json"},
		MaxPayloadBytes: contract.DefaultInlinePortMaxPayloadBytes,
	}
}

func decodeSpec(env contract.ValidateEnv) (Spec, error) {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	spec.Mode = strings.TrimSpace(spec.Mode)
	if spec.Mode == "" {
		spec.Mode = modeRing
	}
	return spec, nil
}

func personTarget(senderUID, recipientUID string) channelport.SendTarget {
	return channelport.SendTarget{
		ChannelID:   recipientUID,
		ChannelType: personChannelType,
		SenderUIDs:  []string{senderUID},
	}
}

// TargetSet is a JSON-friendly send target set.
type TargetSet = channelport.SendTargetSetData
