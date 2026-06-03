package echo

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, Unit{})
}

func TestRunPublishesReportableResult(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "echo", nil, map[string]any{"message": "hello"})
	env.DeclareArtifacts(Unit{}.Definition().Artifacts)
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

func TestRunWritesEchoArtifact(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "echo", nil, map[string]any{"message": "hello artifact"})
	env.DeclareArtifacts(Unit{}.Definition().Artifacts)
	env.SetReportDir(t.TempDir())

	if err := (Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	info := env.Artifacts()["echo.json"]
	if info.ContentType != "application/json" || info.SizeBytes == 0 {
		t.Fatalf("artifact info = %#v", info)
	}
	data, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var got Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if got.Message != "hello artifact" {
		t.Fatalf("artifact message = %q", got.Message)
	}
}
