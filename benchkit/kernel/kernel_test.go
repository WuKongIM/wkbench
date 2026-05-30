package kernel_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/dsl"
	"github.com/WuKongIM/wkbench/benchkit/kernel"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

func TestEngineAutoWiresUniqueMatchingPortsAndRunsInDependencyOrder(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(sourceUnit{calls: &calls})
	reg.MustRegister(sinkUnit{calls: &calls})

	engine := kernel.New(reg)
	result, err := engine.Run(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "auto-wire"},
		Units: map[string]dsl.UnitNode{
			"source": {Use: "test.source"},
			"sink":   {Use: "test.sink"},
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if result.Status != kernel.StatusCompleted {
		t.Fatalf("unexpected status %s", result.Status)
	}
	if len(calls) != 2 || calls[0] != "source" || calls[1] != "sink:source-value" {
		t.Fatalf("unexpected run order/output: %#v", calls)
	}
}

func TestEngineRejectsAmbiguousAutoWire(t *testing.T) {
	reg := registry.New()
	var calls []string
	reg.MustRegister(sourceUnit{kind: "test.source_a/v1", calls: &calls})
	reg.MustRegister(sourceUnit{kind: "test.source_b/v1", calls: &calls})
	reg.MustRegister(sinkUnit{calls: &calls})

	engine := kernel.New(reg)
	err := engine.Validate(context.Background(), dsl.Scenario{
		Version: "wkbench/v2",
		Run:     dsl.RunConfig{ID: "ambiguous"},
		Units: map[string]dsl.UnitNode{
			"a":    {Use: "test.source_a"},
			"b":    {Use: "test.source_b"},
			"sink": {Use: "test.sink"},
		},
	})
	if err == nil {
		t.Fatal("expected ambiguous auto-wire error")
	}
}

const testValuePort = contract.PortType("port.test.value/v1")

type sourceUnit struct {
	kind  string
	calls *[]string
}

func (u sourceUnit) Definition() contract.Definition {
	kind := u.kind
	if kind == "" {
		kind = "test.source/v1"
	}
	return contract.Definition{
		Kind: kind,
		Outputs: []contract.PortDef{
			{Name: "value", Type: testValuePort},
		},
	}
}

func (u sourceUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (u sourceUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (u sourceUnit) Run(ctx context.Context, env contract.RunEnv) error {
	*u.calls = append(*u.calls, env.UnitName())
	return env.SetOutput("value", "source-value")
}

type sinkUnit struct {
	calls *[]string
}

func (u sinkUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.sink/v1",
		Inputs: []contract.PortDef{
			{Name: "input", Type: testValuePort},
		},
	}
}

func (u sinkUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (u sinkUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (u sinkUnit) Run(ctx context.Context, env contract.RunEnv) error {
	value, err := contract.Input[string](env, "input")
	if err != nil {
		return err
	}
	*u.calls = append(*u.calls, env.UnitName()+":"+value)
	return nil
}
