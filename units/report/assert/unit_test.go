package assert_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
	assertunit "github.com/WuKongIM/wkbench/units/report/assert"
)

func TestAssertFailsWhenSendackErrorRateRuleIsViolated(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "limits", map[string]any{
		"summary": trafficport.Summary{SendackOK: 9, SendackErrors: 1},
	}, map[string]any{
		"rules": []map[string]any{
			{"metric": "sendack_error_rate", "op": "eq", "value": 0},
		},
	})

	if err := (assertunit.Unit{}).Run(context.Background(), env); err == nil {
		t.Fatal("expected assertion failure")
	}
}

func TestAssertPassesAndOutputsResult(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "limits", map[string]any{
		"summary": trafficport.Summary{SendackOK: 10},
	}, map[string]any{
		"rules": []map[string]any{
			{"metric": "sendack_error_rate", "op": "eq", "value": 0},
		},
	})

	if err := (assertunit.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	result, err := contract.Output[assertunit.Result](env, "result")
	if err != nil {
		t.Fatalf("result output: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected passed result: %#v", result)
	}
}
