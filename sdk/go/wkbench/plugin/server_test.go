package plugin

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestManifestFromUnitsIncludesPortMetadata(t *testing.T) {
	manifest := ManifestFromUnits("demo.plugin", "0.1.0", []contract.Unit{echoUnit{}})
	if manifest.Name != "demo.plugin" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	if len(manifest.Units) != 1 {
		t.Fatalf("units = %d", len(manifest.Units))
	}
	output := manifest.Units[0].Outputs[0]
	if !output.Meta.Reportable {
		t.Fatalf("output metadata = %#v", output.Meta)
	}
}

type echoUnit struct{}

func (echoUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.echo/v1",
		Outputs: []contract.PortDef{{
			Name: "result",
			Type: "port.demo.echo/v1",
			Meta: contract.PortMeta{Reportable: true},
		}},
	}
}
func (echoUnit) Validate(ctx context.Context, env contract.ValidateEnv) error { return nil }
func (echoUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}
func (echoUnit) Run(ctx context.Context, env contract.RunEnv) error { return nil }
