package echo

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, Unit{})
}

func TestRunPublishesReportableResult(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "echo", nil, map[string]any{"message": "hello"})
	if err := (Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := contract.Output[Result](env, "result")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if got.Message != "hello" {
		t.Fatalf("Message = %q", got.Message)
	}
}
