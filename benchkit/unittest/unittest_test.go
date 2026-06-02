package unittest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
)

func TestAssertUnitContractAcceptsWellFormedDefinition(t *testing.T) {
	unittest.AssertUnitContract(t, goodUnit{})
}

func TestAssertUnitContractRejectsUnversionedKind(t *testing.T) {
	tb := &spyTB{}
	unittest.AssertUnitContract(tb, badKindUnit{})
	if !strings.Contains(tb.message, "kind must end with /vN") {
		t.Fatalf("expected versioned kind failure, got %q", tb.message)
	}
}

func TestAssertUnitContractRejectsUnsafeArtifactNames(t *testing.T) {
	for _, artifactName := range []string{".", "..", "foo/bar", "foo\\bar", "   ", " metrics.jsonl", "metrics.jsonl ", "metrics data.jsonl"} {
		t.Run(artifactName, func(t *testing.T) {
			tb := &spyTB{}
			unittest.AssertUnitContract(tb, artifactNameUnit{name: artifactName})
			if !strings.Contains(tb.message, "artifact") {
				t.Fatalf("expected artifact failure, got %q", tb.message)
			}
		})
	}
}

func TestAssertDeclaredOutputsRejectsMissingOutput(t *testing.T) {
	tb := &spyTB{}
	env := contract.NewTestRunEnv("run", "unit", nil, nil)
	unittest.AssertDeclaredOutputs(tb, outputUnit{}, env)
	if !strings.Contains(tb.message, `declared output "value" was not produced`) {
		t.Fatalf("expected missing output failure, got %q", tb.message)
	}
}

func TestAssertDeclaredOutputsAcceptsProducedReportableOutput(t *testing.T) {
	env := contract.NewTestRunEnv("run", "unit", nil, nil)
	if err := env.SetOutput("value", reportableValue("ok")); err != nil {
		t.Fatal(err)
	}
	unittest.AssertDeclaredOutputs(t, outputUnit{}, env)
}

type spyTB struct {
	message string
}

func (tb *spyTB) Helper() {}

func (tb *spyTB) Fatalf(format string, args ...any) {
	tb.message = strings.TrimSpace(fmt.Sprintf(format, args...))
}

type goodUnit struct{}

func (goodUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        "test.good/v1",
		Title:       "Good unit",
		Description: "A well-formed unit used by tests.",
		Inputs: []contract.PortDef{
			{Name: "input", Type: "port.test.input/v1"},
		},
		Outputs: []contract.PortDef{
			{Name: "output", Type: "port.test.output/v1"},
		},
		Metrics: []contract.MetricDef{
			{Name: "attempt_total", Type: "counter"},
		},
		Artifacts: []contract.ArtifactDef{
			{Name: "debug"},
		},
	}
}

func (goodUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }

func (goodUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (goodUnit) Run(context.Context, contract.RunEnv) error { return nil }

type badKindUnit struct{ goodUnit }

func (badKindUnit) Definition() contract.Definition {
	def := goodUnit{}.Definition()
	def.Kind = "test.bad"
	return def
}

type outputUnit struct{ goodUnit }

func (outputUnit) Definition() contract.Definition {
	def := goodUnit{}.Definition()
	def.Outputs = []contract.PortDef{{Name: "value", Type: "port.test.value/v1"}}
	return def
}

type artifactNameUnit struct {
	goodUnit
	name string
}

func (u artifactNameUnit) Definition() contract.Definition {
	def := goodUnit{}.Definition()
	def.Artifacts = []contract.ArtifactDef{{Name: u.name}}
	return def
}

type reportableValue string

func (v reportableValue) ReportOutput() any {
	return string(v)
}
