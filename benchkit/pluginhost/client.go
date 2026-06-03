package pluginhost

import (
	"context"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

type Client interface {
	Validate(context.Context, UnitRequest) error
	Plan(context.Context, UnitRequest) (contract.Plan, error)
	Run(context.Context, RunRequest, contract.RunEnv) error
}

type UnitRequest struct {
	PluginName        string
	UnitName          string
	Kind              string
	RunID             string
	RunDurationMillis int64
	WorkerCount       int
	SpecJSON          []byte
}

type RunRequest struct {
	UnitRequest
	Inputs map[string]any
}
