// Package unittest provides reusable assertions for wkbench unit authors.
package unittest

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

// TB is the small testing surface required by this package.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// AssertUnitContract verifies the stable shape every wkbench unit should expose.
func AssertUnitContract(t TB, unit contract.Unit) {
	t.Helper()
	if unit == nil {
		t.Fatalf("unit must not be nil")
		return
	}
	def := unit.Definition()
	if !hasVersionSuffix(def.Kind) {
		t.Fatalf("unit kind must end with /vN, got %q", def.Kind)
		return
	}
	if strings.TrimSpace(def.Title) == "" {
		t.Fatalf("unit %q title is required", def.Kind)
		return
	}
	if strings.TrimSpace(def.Description) == "" {
		t.Fatalf("unit %q description is required", def.Kind)
		return
	}
	assertPorts(t, def.Kind, "input", def.Inputs)
	assertPorts(t, def.Kind, "output", def.Outputs)
	assertMetrics(t, def.Kind, def.Metrics)
	assertArtifacts(t, def.Kind, def.Artifacts)
}

// AssertDeclaredOutputs verifies that a unit test produced every declared output.
func AssertDeclaredOutputs(t TB, unit contract.Unit, outputs contract.OutputReader) {
	t.Helper()
	if unit == nil {
		t.Fatalf("unit must not be nil")
		return
	}
	if outputs == nil {
		t.Fatalf("output reader must not be nil")
		return
	}
	for _, output := range unit.Definition().Outputs {
		value, ok := outputs.Output(output.Name)
		if !ok {
			t.Fatalf("declared output %q was not produced", output.Name)
			return
		}
		if reportable, ok := value.(contract.ReportableOutput); ok {
			if _, err := json.Marshal(reportable.ReportOutput()); err != nil {
				t.Fatalf("declared output %q report value is not JSON-friendly: %v", output.Name, err)
				return
			}
		}
	}
}

func assertPorts(t TB, kind, label string, ports []contract.PortDef) {
	t.Helper()
	seen := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		name := strings.TrimSpace(port.Name)
		if name == "" {
			t.Fatalf("unit %q %s port name is required", kind, label)
			return
		}
		if _, ok := seen[name]; ok {
			t.Fatalf("unit %q duplicate %s port %q", kind, label, name)
			return
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(string(port.Type)) == "" {
			t.Fatalf("unit %q %s port %q type is required", kind, label, name)
			return
		}
	}
}

func assertMetrics(t TB, kind string, metrics []contract.MetricDef) {
	t.Helper()
	seen := make(map[string]struct{}, len(metrics))
	for _, metric := range metrics {
		name := strings.TrimSpace(metric.Name)
		if name == "" {
			t.Fatalf("unit %q metric name is required", kind)
			return
		}
		if _, ok := seen[name]; ok {
			t.Fatalf("unit %q duplicate metric %q", kind, name)
			return
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(metric.Type) == "" {
			t.Fatalf("unit %q metric %q type is required", kind, name)
			return
		}
	}
}

func assertArtifacts(t TB, kind string, artifacts []contract.ArtifactDef) {
	t.Helper()
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		name := strings.TrimSpace(artifact.Name)
		if name == "" {
			t.Fatalf("unit %q artifact name is required", kind)
			return
		}
		if _, ok := seen[name]; ok {
			t.Fatalf("unit %q duplicate artifact %q", kind, name)
			return
		}
		seen[name] = struct{}{}
	}
}

func hasVersionSuffix(kind string) bool {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return false
	}
	idx := strings.LastIndex(kind, "/v")
	if idx < 0 || idx+2 >= len(kind) {
		return false
	}
	for _, r := range kind[idx+2:] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
