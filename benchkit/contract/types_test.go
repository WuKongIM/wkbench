package contract_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestTestRunEnvRecordsDurationObservations(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "traffic", nil, nil)
	env.ObserveDuration("sendack_latency", 10*time.Millisecond, nil)
	env.ObserveDuration("sendack_latency", 20*time.Millisecond, nil)

	values := env.DurationValues("sendack_latency")
	if len(values) != 2 || values[0] != 10*time.Millisecond || values[1] != 20*time.Millisecond {
		t.Fatalf("unexpected duration values: %#v", values)
	}
	values[0] = time.Hour
	if env.DurationValues("sendack_latency")[0] != 10*time.Millisecond {
		t.Fatal("DurationValues must return a copy")
	}
}

func TestBackgroundInterfacesCompile(t *testing.T) {
	var _ contract.BackgroundUnit = backgroundCompileUnit{}
	var _ contract.BackgroundTask = backgroundCompileTask{}
}

type backgroundCompileUnit struct{}

func (backgroundCompileUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.background/v1"}
}
func (backgroundCompileUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (backgroundCompileUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (backgroundCompileUnit) Run(context.Context, contract.RunEnv) error { return nil }
func (backgroundCompileUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	return backgroundCompileTask{}, nil
}

type backgroundCompileTask struct{}

func (backgroundCompileTask) Done() <-chan error {
	ch := make(chan error)
	close(ch)
	return ch
}
func (backgroundCompileTask) Stop(context.Context) error { return nil }
