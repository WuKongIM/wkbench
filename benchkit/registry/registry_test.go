package registry_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

func TestRegistryResolvesDefaultVersionWhenUnique(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(stubUnit{kind: "traffic.group_send/v1"})

	unit, def, err := reg.Resolve("traffic.group_send")
	if err != nil {
		t.Fatalf("resolve default version: %v", err)
	}
	if def.Kind != "traffic.group_send/v1" {
		t.Fatalf("unexpected kind %q", def.Kind)
	}
	if unit == nil {
		t.Fatal("expected resolved unit")
	}
}

func TestRegistryRejectsAmbiguousDefaultVersion(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(stubUnit{kind: "traffic.group_send/v1"})
	reg.MustRegister(stubUnit{kind: "traffic.group_send/v2"})

	if _, _, err := reg.Resolve("traffic.group_send"); err == nil {
		t.Fatal("expected ambiguous default version error")
	}
}

type stubUnit struct {
	kind string
}

func (u stubUnit) Definition() contract.Definition {
	return contract.Definition{Kind: u.kind}
}

func (u stubUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (u stubUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (u stubUnit) Run(context.Context, contract.RunEnv) error {
	return nil
}
