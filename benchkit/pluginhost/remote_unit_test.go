package pluginhost

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestRemoteUnitDelegatesValidatePlanAndRun(t *testing.T) {
	client := &fakeClient{}
	var unit contract.Unit = NewRemoteUnit(client, Unit{
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

func TestNewRemoteUnitDoesNotImplementBackgroundWhenManifestDoesNotMarkIt(t *testing.T) {
	unit := NewRemoteUnit(&fakeClient{}, Unit{
		Kind: "test.normal/v1",
	})

	if _, ok := unit.(contract.BackgroundUnit); ok {
		t.Fatalf("non-background remote unit implements BackgroundUnit")
	}
}

func TestNewRemoteUnitImplementsBackgroundWhenManifestMarksIt(t *testing.T) {
	unit := NewRemoteUnit(&fakeClient{}, Unit{
		Kind:       "test.background/v1",
		Background: true,
	})

	if _, ok := unit.(contract.BackgroundUnit); !ok {
		t.Fatalf("background remote unit does not implement BackgroundUnit")
	}
}

func TestRemoteUnitAliasExposesDefinitionKindButSendsOriginalKind(t *testing.T) {
	client := &fakeClient{}
	var unit contract.Unit = NewRemoteUnitAlias(client, Unit{
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
	var unit contract.Unit = NewRemoteUnit(client, Unit{
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
	var unit contract.Unit = NewRemoteUnit(client, Unit{
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

func TestRemoteUnitRunPassesInputSourceDefsWhenEnvProvidesThem(t *testing.T) {
	client := &fakeClient{}
	sourceDef := contract.PortDef{
		Name: "result",
		Type: "port.demo.message/v1",
		Meta: contract.PortMeta{
			Boundary:        contract.PortBoundaryData,
			Transport:       contract.PortTransportInline,
			Encodings:       []string{"json"},
			MaxPayloadBytes: 32,
		},
	}
	var unit contract.Unit = NewRemoteUnit(client, Unit{
		PluginName: "demo",
		Kind:       "demo.echo/v1",
		Inputs: []contract.PortDef{{
			Name: "message",
			Type: "port.demo.message/v1",
		}},
	})
	env := sourceMetadataEnv{
		TestRunEnv: contract.NewTestRunEnv("run-1", "echo", map[string]any{"message": "hi"}, nil),
		sources:    map[string]contract.PortDef{"message": sourceDef},
	}

	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, ok := client.runReq.InputSourceDefs["message"]
	if !ok {
		t.Fatalf("source defs missing: %#v", client.runReq.InputSourceDefs)
	}
	if got.Name != "result" || got.Type != sourceDef.Type || got.Meta.MaxPayloadBytes != 32 {
		t.Fatalf("source def = %#v", got)
	}
}

func TestRemoteBackgroundUnitDelegatesStart(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnit(client, Unit{
		Kind:       "test.background_start/v1",
		Background: true,
		Inputs: []contract.PortDef{{
			Name: "input",
			Type: "test.input/v1",
			Meta: contract.PortMeta{
				Boundary:        contract.PortBoundaryData,
				Transport:       contract.PortTransportInline,
				Encodings:       []string{"json"},
				MaxPayloadBytes: 1024,
			},
		}},
	})
	background, ok := unit.(contract.BackgroundUnit)
	if !ok {
		t.Fatalf("remote unit did not implement BackgroundUnit")
	}
	env := contract.NewTestRunEnv("run-1", "background", map[string]any{"input": map[string]any{"ok": true}}, nil)

	task, err := background.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task == nil {
		t.Fatalf("start task is nil")
	}
	if client.startReq.Kind != "test.background_start/v1" {
		t.Fatalf("start kind = %q", client.startReq.Kind)
	}
	if _, ok := client.startReq.Inputs["input"]; !ok {
		t.Fatalf("start inputs missing input")
	}
}

type sourceMetadataEnv struct {
	*contract.TestRunEnv
	sources map[string]contract.PortDef
}

func (e sourceMetadataEnv) InputSourcePort(name string) (contract.PortDef, bool) {
	def, ok := e.sources[name]
	return def, ok
}

type fakeClient struct {
	validateCalled bool
	planCalled     bool
	runCalled      bool
	startCalled    bool
	validateReq    UnitRequest
	planReq        UnitRequest
	runReq         RunRequest
	startReq       StartRequest
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

func (f *fakeClient) Start(ctx context.Context, req StartRequest, env contract.RunEnv) (contract.BackgroundTask, error) {
	f.startCalled = true
	f.startReq = req
	return noopBackgroundTask{}, nil
}

type noopBackgroundTask struct{}

func (noopBackgroundTask) Stop(context.Context) error { return nil }

func (noopBackgroundTask) Done() <-chan error {
	ch := make(chan error)
	return ch
}
