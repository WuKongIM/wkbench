package pluginhost

import (
	"context"
	"encoding/json"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

type RemoteUnit struct {
	client    Client
	unit      Unit
	aliasKind string
}

func NewRemoteUnit(client Client, unit Unit) RemoteUnit {
	return RemoteUnit{client: client, unit: unit}
}

func NewRemoteUnitAlias(client Client, unit Unit, aliasKind string) RemoteUnit {
	return RemoteUnit{client: client, unit: unit, aliasKind: aliasKind}
}

func (u RemoteUnit) Definition() contract.Definition {
	def := u.unit.Definition()
	if u.aliasKind != "" {
		def.Kind = u.aliasKind
	}
	return def
}

func (u RemoteUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := encodeSpec(env)
	if err != nil {
		return err
	}
	return u.client.Validate(ctx, UnitRequest{
		PluginName: u.unit.PluginName,
		UnitName:   env.UnitName(),
		Kind:       u.unit.Kind,
		SpecJSON:   spec,
	})
}

func (u RemoteUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	spec, err := encodeSpec(env)
	if err != nil {
		return contract.Plan{}, err
	}
	return u.client.Plan(ctx, UnitRequest{
		PluginName:        u.unit.PluginName,
		UnitName:          env.UnitName(),
		Kind:              u.unit.Kind,
		RunID:             env.RunID(),
		RunDurationMillis: env.RunDuration().Milliseconds(),
		WorkerCount:       env.WorkerCount(),
		SpecJSON:          spec,
	})
}

func (u RemoteUnit) Run(ctx context.Context, env contract.RunEnv) error {
	spec, err := encodeSpec(env)
	if err != nil {
		return err
	}
	inputs, err := collectInputs(u.unit.Definition().Inputs, env)
	if err != nil {
		return err
	}
	return u.client.Run(ctx, RunRequest{
		UnitRequest: UnitRequest{
			PluginName:        u.unit.PluginName,
			UnitName:          env.UnitName(),
			Kind:              u.unit.Kind,
			RunID:             env.RunID(),
			RunDurationMillis: env.RunDuration().Milliseconds(),
			WorkerCount:       env.WorkerCount(),
			SpecJSON:          spec,
		},
		Inputs: inputs,
	}, env)
}

func collectInputs(defs []contract.PortDef, env contract.RunEnv) (map[string]any, error) {
	inputs := make(map[string]any, len(defs))
	for _, def := range defs {
		value, err := env.Input(def.Name)
		if err != nil {
			if def.Optional {
				continue
			}
			return nil, err
		}
		inputs[def.Name] = value
	}
	return inputs, nil
}

func encodeSpec(env contract.ValidateEnv) ([]byte, error) {
	var spec map[string]any
	if err := env.DecodeSpec(&spec); err != nil {
		return nil, err
	}
	return json.Marshal(spec)
}
