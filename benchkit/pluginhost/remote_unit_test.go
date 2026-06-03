package pluginhost

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestRemoteUnitDelegatesValidatePlanAndRun(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnit(client, Unit{
		PluginName: "demo",
		Kind:       "demo.echo/v1",
		Title:      "Echo",
		Inputs: []contract.PortDef{{
			Name: "message",
			Type: "port.demo.message/v1",
		}},
	})
	env := contract.NewTestRunEnv("run-1", "echo", map[string]any{"message": "hi"}, nil)

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
	if got := client.runReq.Inputs["message"]; got != "hi" {
		t.Fatalf("run input message = %#v", got)
	}
	if !client.validateCalled || !client.planCalled || !client.runCalled {
		t.Fatalf("client calls missing: %#v", client)
	}
}

func TestRemoteUnitAliasExposesDefinitionKindButSendsOriginalKind(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnitAlias(client, Unit{
		PluginName: "wkbench.demo",
		Kind:       "demo.echo/v1",
		Title:      "Echo",
	}, "wkbench.demo:demo.echo/v1")
	if got := unit.Definition().Kind; got != "wkbench.demo:demo.echo/v1" {
		t.Fatalf("Definition().Kind = %q", got)
	}
	env := contract.NewTestRunEnv("run-1", "echo", nil, map[string]any{"message": "hi"})

	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := unit.Plan(context.Background(), env); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}

	for name, got := range map[string]string{
		"validate": client.validateReq.Kind,
		"plan":     client.planReq.Kind,
		"run":      client.runReq.Kind,
	} {
		if got != "demo.echo/v1" {
			t.Fatalf("%s request kind = %q", name, got)
		}
	}
}

func TestRemoteUnitRunReturnsMissingRequiredInput(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnit(client, Unit{
		PluginName: "demo",
		Kind:       "demo.echo/v1",
		Inputs: []contract.PortDef{{
			Name: "message",
			Type: "port.demo.message/v1",
		}},
	})
	env := contract.NewTestRunEnv("run-1", "echo", nil, nil)

	if err := unit.Run(context.Background(), env); err == nil {
		t.Fatal("expected missing input error")
	}
	if client.runCalled {
		t.Fatal("client run called despite missing required input")
	}
}

func TestRemoteUnitRunIgnoresMissingOptionalInput(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnit(client, Unit{
		PluginName: "demo",
		Kind:       "demo.echo/v1",
		Inputs: []contract.PortDef{{
			Name:     "message",
			Type:     "port.demo.message/v1",
			Optional: true,
		}},
	})
	env := contract.NewTestRunEnv("run-1", "echo", nil, nil)

	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !client.runCalled {
		t.Fatal("client run was not called")
	}
	if _, ok := client.runReq.Inputs["message"]; ok {
		t.Fatalf("optional input was forwarded: %#v", client.runReq.Inputs)
	}
}

type fakeClient struct {
	validateCalled bool
	planCalled     bool
	runCalled      bool
	validateReq    UnitRequest
	planReq        UnitRequest
	runReq         RunRequest
}

func (f *fakeClient) Validate(ctx context.Context, req UnitRequest) error {
	f.validateCalled = true
	f.validateReq = req
	return nil
}

func (f *fakeClient) Plan(ctx context.Context, req UnitRequest) (contract.Plan, error) {
	f.planCalled = true
	f.planReq = req
	return contract.Plan{UnitName: req.UnitName}, nil
}

func (f *fakeClient) Run(ctx context.Context, req RunRequest, env contract.RunEnv) error {
	f.runCalled = true
	f.runReq = req
	got, ok := req.Inputs["message"]
	if !ok {
		return env.SetOutput("result", "missing")
	}
	message, ok := got.(string)
	if !ok {
		return env.SetOutput("result", "not-string")
	}
	return env.SetOutput("result", message)
}
