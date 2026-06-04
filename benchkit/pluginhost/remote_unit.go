package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

type remoteRunnable interface {
	contract.Unit
}

type RemoteUnit struct {
	client    Client
	unit      Unit
	aliasKind string
}

type remoteBackgroundUnit struct {
	RemoteUnit
}

func NewRemoteUnit(client Client, unit Unit) contract.Unit {
	base := RemoteUnit{client: client, unit: unit}
	if unit.Background {
		return remoteBackgroundUnit{RemoteUnit: base}
	}
	return base
}

func NewRemoteUnitAlias(client Client, unit Unit, aliasKind string) contract.Unit {
	base := RemoteUnit{client: client, unit: unit, aliasKind: aliasKind}
	if unit.Background {
		return remoteBackgroundUnit{RemoteUnit: base}
	}
	return base
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
	inputSourceDefs, err := collectInputSourceDefs(env, inputs)
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
		InputDefs:       u.unit.Definition().Inputs,
		InputSourceDefs: inputSourceDefs,
		Inputs:          inputs,
	}, env)
}

func (u remoteBackgroundUnit) Start(ctx context.Context, env contract.RunEnv) (contract.BackgroundTask, error) {
	spec, err := encodeSpec(env)
	if err != nil {
		return nil, err
	}
	inputs, err := collectInputs(u.unit.Definition().Inputs, env)
	if err != nil {
		return nil, err
	}
	inputSourceDefs, err := collectInputSourceDefs(env, inputs)
	if err != nil {
		return nil, err
	}
	return u.client.Start(ctx, StartRequest{RunRequest: RunRequest{
		UnitRequest: UnitRequest{
			PluginName:        u.unit.PluginName,
			UnitName:          env.UnitName(),
			Kind:              u.unit.Kind,
			RunID:             env.RunID(),
			RunDurationMillis: env.RunDuration().Milliseconds(),
			WorkerCount:       env.WorkerCount(),
			SpecJSON:          spec,
		},
		InputDefs:       u.unit.Definition().Inputs,
		InputSourceDefs: inputSourceDefs,
		Inputs:          inputs,
	}}, env)
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

func collectInputSourceDefs(env contract.RunEnv, inputs map[string]any) (map[string]contract.PortDef, error) {
	provider, ok := env.(contract.InputSourcePortProvider)
	if !ok || len(inputs) == 0 {
		return nil, nil
	}
	out := make(map[string]contract.PortDef, len(inputs))
	for name := range inputs {
		def, ok := provider.InputSourcePort(name)
		if !ok {
			return nil, fmt.Errorf("input %q source port metadata not found", name)
		}
		out[name] = def
	}
	return out, nil
}

func encodeSpec(env contract.ValidateEnv) ([]byte, error) {
	var spec map[string]any
	if err := env.DecodeSpec(&spec); err != nil {
		return nil, err
	}
	return json.Marshal(spec)
}
