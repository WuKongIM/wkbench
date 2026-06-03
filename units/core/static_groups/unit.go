// Package staticgroups provides deterministic in-memory group channels.
package staticgroups

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const kind = "core.static_groups/v1"

// Unit produces a deterministic group set without network IO.
type Unit struct{}

// Spec configures deterministic group generation.
type Spec struct {
	// Count is the number of generated group channels.
	Count int `json:"count" yaml:"count"`
	// MembersPerChannel is the number of generated members in each channel.
	MembersPerChannel int `json:"members_per_channel" yaml:"members_per_channel"`
	// ChannelPrefix is prepended to generated channel ids.
	ChannelPrefix string `json:"channel_prefix" yaml:"channel_prefix"`
	// UIDPrefix is prepended to generated user ids.
	UIDPrefix string `json:"uid_prefix" yaml:"uid_prefix"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Static group channels",
		Description: "Produces deterministic in-memory group channels for examples and tests.",
		Outputs: []contract.PortDef{
			{Name: "groups", Type: channelport.GroupSetV1, Meta: inlineJSONDataMeta()},
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
	if spec.MembersPerChannel <= 0 {
		return fmt.Errorf("members_per_channel must be greater than zero")
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
	groups := make([]channelport.GroupChannel, 0, spec.Count)
	userIndex := 0
	for channelIndex := 0; channelIndex < spec.Count; channelIndex++ {
		members := make([]string, 0, spec.MembersPerChannel)
		for member := 0; member < spec.MembersPerChannel; member++ {
			members = append(members, fmt.Sprintf("%s-%d", spec.UIDPrefix, userIndex))
			userIndex++
		}
		groups = append(groups, channelport.GroupChannel{
			ChannelID: fmt.Sprintf("%s-%d", spec.ChannelPrefix, channelIndex),
			Members:   members,
		})
	}
	return env.SetOutput("groups", GroupSet{Items: groups})
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
	spec := Spec{ChannelPrefix: "group", UIDPrefix: "user"}
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	spec.ChannelPrefix = strings.TrimSpace(spec.ChannelPrefix)
	if spec.ChannelPrefix == "" {
		spec.ChannelPrefix = "group"
	}
	spec.UIDPrefix = strings.TrimSpace(spec.UIDPrefix)
	if spec.UIDPrefix == "" {
		spec.UIDPrefix = "user"
	}
	return spec, nil
}

// GroupSet is a JSON-friendly channel set that implements channel.GroupSet.
type GroupSet = channelport.GroupSetData
