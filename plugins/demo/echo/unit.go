package echo

import (
	"context"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

const kind = "demo.echo/v1"

type Unit struct{}

type Spec struct {
	Message string `json:"message"`
}

type Result struct {
	Message string `json:"message"`
}

func (r Result) ReportOutput() any {
	return r
}

func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Demo echo",
		Description: "Echoes a message through an external plugin.",
		Outputs: []contract.PortDef{{
			Name: "result",
			Type: "port.demo.echo/v1",
			Meta: contract.PortMeta{
				Boundary:   contract.PortBoundaryData,
				Transport:  contract.PortTransportInline,
				Reportable: true,
			},
		}},
	}
}

func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	var spec Spec
	return env.DecodeSpec(&spec)
}

func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return err
	}
	return env.SetOutput("result", Result{Message: spec.Message})
}
