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

func TestManifestFromUnitsCopiesDefinitionSlices(t *testing.T) {
	source := contract.Definition{
		Kind: "demo.mutable/v1",
		Inputs: []contract.PortDef{{
			Name: "request",
			Type: "port.demo.request/v1",
			Meta: contract.PortMeta{
				Encodings:  []string{"json"},
				Operations: []string{"read"},
			},
		}},
		Outputs: []contract.PortDef{{
			Name: "response",
			Type: "port.demo.response/v1",
			Meta: contract.PortMeta{
				Encodings:  []string{"msgpack"},
				Operations: []string{"write"},
			},
		}},
		Metrics: []contract.MetricDef{{
			Name: "requests",
			Type: "counter",
		}},
		Artifacts: []contract.ArtifactDef{{
			Name:        "summary",
			ContentType: "application/json",
		}},
	}
	manifest := ManifestFromUnits("demo.plugin", "0.1.0", []contract.Unit{mutableUnit{def: source}})

	source.Inputs[0].Name = "changed-request"
	source.Inputs[0].Meta.Encodings[0] = "changed-json"
	source.Inputs[0].Meta.Operations[0] = "changed-read"
	source.Outputs[0].Name = "changed-response"
	source.Outputs[0].Meta.Encodings[0] = "changed-msgpack"
	source.Outputs[0].Meta.Operations[0] = "changed-write"
	source.Metrics[0].Name = "changed-requests"
	source.Artifacts[0].Name = "changed-summary"

	unit := manifest.Units[0]
	if unit.Inputs[0].Name != "request" || unit.Inputs[0].Meta.Encodings[0] != "json" || unit.Inputs[0].Meta.Operations[0] != "read" {
		t.Fatalf("input was not isolated from source mutation: %#v", unit.Inputs[0])
	}
	if unit.Outputs[0].Name != "response" || unit.Outputs[0].Meta.Encodings[0] != "msgpack" || unit.Outputs[0].Meta.Operations[0] != "write" {
		t.Fatalf("output was not isolated from source mutation: %#v", unit.Outputs[0])
	}
	if unit.Metrics[0].Name != "requests" {
		t.Fatalf("metric was not isolated from source mutation: %#v", unit.Metrics[0])
	}
	if unit.Artifacts[0].Name != "summary" {
		t.Fatalf("artifact was not isolated from source mutation: %#v", unit.Artifacts[0])
	}

	manifest.Units[0].Inputs[0].Name = "manifest-request"
	manifest.Units[0].Inputs[0].Meta.Encodings[0] = "manifest-json"
	manifest.Units[0].Inputs[0].Meta.Operations[0] = "manifest-read"
	manifest.Units[0].Outputs[0].Name = "manifest-response"
	manifest.Units[0].Outputs[0].Meta.Encodings[0] = "manifest-msgpack"
	manifest.Units[0].Outputs[0].Meta.Operations[0] = "manifest-write"
	manifest.Units[0].Metrics[0].Name = "manifest-requests"
	manifest.Units[0].Artifacts[0].Name = "manifest-summary"
	if source.Inputs[0].Name != "changed-request" || source.Inputs[0].Meta.Encodings[0] != "changed-json" || source.Inputs[0].Meta.Operations[0] != "changed-read" {
		t.Fatalf("source input was changed by manifest mutation: %#v", source.Inputs[0])
	}
	if source.Outputs[0].Name != "changed-response" || source.Outputs[0].Meta.Encodings[0] != "changed-msgpack" || source.Outputs[0].Meta.Operations[0] != "changed-write" {
		t.Fatalf("source output was changed by manifest mutation: %#v", source.Outputs[0])
	}
	if source.Metrics[0].Name != "changed-requests" {
		t.Fatalf("source metric was changed by manifest mutation: %#v", source.Metrics[0])
	}
	if source.Artifacts[0].Name != "changed-summary" {
		t.Fatalf("source artifact was changed by manifest mutation: %#v", source.Artifacts[0])
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

type mutableUnit struct {
	def contract.Definition
}

func (u mutableUnit) Definition() contract.Definition { return u.def }
func (mutableUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (u mutableUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}
func (mutableUnit) Run(ctx context.Context, env contract.RunEnv) error { return nil }
