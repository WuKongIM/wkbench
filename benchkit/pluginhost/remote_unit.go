package pluginhost

import (
	"context"
	"encoding/json"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

type RemoteUnit struct {
	client Client
	unit   Unit
}

func NewRemoteUnit(client Client, unit Unit) RemoteUnit {
	return RemoteUnit{client: client, unit: unit}
}

func (u RemoteUnit) Definition() contract.Definition {
	return u.unit.Definition()
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
	}, env)
}

func encodeSpec(env contract.ValidateEnv) ([]byte, error) {
	var spec map[string]any
	if err := env.DecodeSpec(&spec); err != nil {
		return nil, err
	}
	return json.Marshal(spec)
}
