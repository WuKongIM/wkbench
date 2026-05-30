// Package preparegroupchannels implements wukongim.prepare_group_channels/v1.
package preparegroupchannels

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	channelport "github.com/WuKongIM/wkbench/benchkit/ports/channel"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	"github.com/WuKongIM/wkbench/benchkit/registry"
	"github.com/WuKongIM/wkbench/units/wukongim/internal/benchapi"
)

const (
	kind             = "wukongim.prepare_group_channels/v1"
	groupChannelType = uint8(2)
	overlapAllowed   = "allowed"
	overlapDisallow  = "disallowed"
)

// Unit prepares group channels and subscribers through /bench/v1.
type Unit struct{}

// Spec configures group preparation.
type Spec struct {
	// Profile is used in generated channel ids.
	Profile string `json:"profile" yaml:"profile"`
	// Count is the number of generated groups.
	Count int `json:"count" yaml:"count"`
	// MembersPerChannel is the number of subscribers per group.
	MembersPerChannel int `json:"members_per_channel" yaml:"members_per_channel"`
	// Overlap is allowed or disallowed.
	Overlap string `json:"overlap" yaml:"overlap"`
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
		Title:       "WuKongIM prepare group channels",
		Description: "Creates benchmark group channels and subscribers through the target bench API.",
		Inputs: []contract.PortDef{
			{Name: "target", Type: targetport.TargetV1},
			{Name: "identities", Type: identityport.PoolV1},
		},
		Outputs: []contract.PortDef{
			{Name: "channels", Type: channelport.GroupSetV1},
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
	if spec.Overlap != overlapAllowed && spec.Overlap != overlapDisallow {
		return fmt.Errorf("overlap must be allowed or disallowed")
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
	tgt, err := contract.Input[targetport.Target](env, "target")
	if err != nil {
		return err
	}
	pool, err := contract.Input[identityport.Pool](env, "identities")
	if err != nil {
		return err
	}
	groups, err := buildGroups(env.RunID(), spec, pool)
	if err != nil {
		return err
	}
	channels := make([]benchapi.ChannelItem, 0, len(groups.Items))
	subscribers := make([]benchapi.SubscriberItem, 0, len(groups.Items))
	for _, group := range groups.Items {
		channels = append(channels, benchapi.ChannelItem{ChannelID: group.ChannelID, ChannelType: groupChannelType})
		subscribers = append(subscribers, benchapi.SubscriberItem{ChannelID: group.ChannelID, ChannelType: groupChannelType, Subscribers: group.Members})
	}
	client := benchapi.NewClient(benchapi.Config{APIAddrs: tgt.APIAddrs, Token: tgt.BenchAPIToken})
	if err := client.UpsertChannels(ctx, benchapi.BatchChannelsRequest{
		RunID:    env.RunID(),
		BatchID:  env.UnitName() + "-channels-0",
		Upsert:   true,
		Channels: channels,
	}); err != nil {
		return err
	}
	if err := client.AddSubscribers(ctx, benchapi.BatchSubscribersRequest{
		RunID:   env.RunID(),
		BatchID: env.UnitName() + "-subscribers-0",
		Items:   subscribers,
	}); err != nil {
		return err
	}
	return env.SetOutput("channels", groups)
}

func decodeSpec(env contract.ValidateEnv) (Spec, error) {
	spec := Spec{Profile: "group", Overlap: overlapAllowed}
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	spec.Profile = strings.TrimSpace(spec.Profile)
	if spec.Profile == "" {
		spec.Profile = "group"
	}
	spec.Overlap = strings.TrimSpace(spec.Overlap)
	if spec.Overlap == "" {
		spec.Overlap = overlapAllowed
	}
	return spec, nil
}

func buildGroups(runID string, spec Spec, pool identityport.Pool) (GroupSet, error) {
	required := spec.MembersPerChannel
	if spec.Overlap == overlapDisallow {
		required = spec.Count * spec.MembersPerChannel
	}
	if pool.Count() < required {
		return GroupSet{}, fmt.Errorf("identity pool has %d users but %d are required", pool.Count(), required)
	}
	groups := make([]channelport.GroupChannel, 0, spec.Count)
	cursor := 0
	for channelIndex := 0; channelIndex < spec.Count; channelIndex++ {
		members := make([]string, 0, spec.MembersPerChannel)
		for memberIndex := 0; memberIndex < spec.MembersPerChannel; memberIndex++ {
			identityIndex := memberIndex
			if spec.Overlap == overlapDisallow {
				identityIndex = cursor
				cursor++
			}
			members = append(members, pool.At(identityIndex).UID)
		}
		groups = append(groups, channelport.GroupChannel{
			ChannelID: fmt.Sprintf("%s-%s-%d", runID, spec.Profile, channelIndex),
			Members:   members,
		})
	}
	return GroupSet{Items: groups}, nil
}

// GroupSet is a JSON-friendly prepared group set.
type GroupSet struct {
	// Items contains prepared group channels.
	Items []channelport.GroupChannel `json:"items"`
}

// Count implements channel.GroupSet.
func (s GroupSet) Count() int { return len(s.Items) }

// At implements channel.GroupSet.
func (s GroupSet) At(index int) channelport.GroupChannel { return s.Items[index] }
