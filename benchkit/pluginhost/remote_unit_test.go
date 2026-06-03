package pluginhost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestRemoteUnitDelegatesValidatePlanAndRun(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnit(client, Unit{PluginName: "demo", Kind: "demo.echo/v1", Title: "Echo"})
	env := contract.NewTestRunEnv("run-1", "echo", nil, map[string]any{"message": "hi"})

	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	plan, err := unit.Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.UnitName != "echo" {
		t.Fatalf("UnitName = %q", plan.UnitName)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := contract.Output[string](env, "result")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if got != "hi" {
		t.Fatalf("result = %q", got)
	}
	if !client.validateCalled || !client.planCalled || !client.runCalled {
		t.Fatalf("client calls missing: %#v", client)
	}
}

type fakeClient struct {
	validateCalled bool
	planCalled     bool
	runCalled      bool
}

func (f *fakeClient) Validate(ctx context.Context, req UnitRequest) error {
	f.validateCalled = true
	return nil
}

func (f *fakeClient) Plan(ctx context.Context, req UnitRequest) (contract.Plan, error) {
	f.planCalled = true
	return contract.Plan{UnitName: req.UnitName}, nil
}

func (f *fakeClient) Run(ctx context.Context, req RunRequest, env contract.RunEnv) error {
	f.runCalled = true
	var spec struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(req.SpecJSON, &spec); err != nil {
		return err
	}
	return env.SetOutput("result", spec.Message)
}
