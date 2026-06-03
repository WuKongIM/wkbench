package pluginhost

import (
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestCatalogResolvesUniqueKind(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "acme.system", Version: "0.1.0", Units: []Unit{{Kind: "acme.echo/v1"}}},
	})
	unit, err := catalog.Resolve("acme.echo/v1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if unit.PluginName != "acme.system" || unit.Kind != "acme.echo/v1" {
		t.Fatalf("unexpected unit: %#v", unit)
	}
}

func TestCatalogRejectsAmbiguousUnqualifiedKind(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "first", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
		{Name: "second", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
	})
	_, err := catalog.Resolve("demo.echo/v1")
	if err == nil {
		t.Fatal("expected ambiguity")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q, want ambiguous", err.Error())
	}
}

func TestCatalogResolvesExplicitPluginKind(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "first", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
		{Name: "second", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
	})
	unit, err := catalog.Resolve("second:demo.echo/v1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if unit.PluginName != "second" {
		t.Fatalf("PluginName = %q", unit.PluginName)
	}
}

func TestCatalogRejectsEmptyQualifiedComponents(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "plugin", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
	})
	for _, use := range []string{":demo.echo/v1", "plugin:", " : demo.echo/v1", "plugin: "} {
		_, err := catalog.Resolve(use)
		if err == nil {
			t.Fatalf("Resolve(%q) succeeded, want error", use)
		}
		if !strings.Contains(err.Error(), "required") {
			t.Fatalf("Resolve(%q) error = %q, want required", use, err.Error())
		}
	}
}

func TestCatalogTrimsQualifiedComponents(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "first", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
		{Name: "second", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
	})
	unit, err := catalog.Resolve(" second : demo.echo/v1 ")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if unit.PluginName != "second" || unit.Kind != "demo.echo/v1" {
		t.Fatalf("unexpected unit: %#v", unit)
	}
}

func TestCatalogRejectsDuplicateExplicitPluginKind(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "plugin", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
		{Name: "plugin", Version: "0.2.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
	})
	_, err := catalog.Resolve("plugin:demo.echo/v1")
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %q, want duplicate", err.Error())
	}
}

func TestCatalogPreservesPortMetadata(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{
			Name: "acme.system",
			Units: []Unit{{
				Kind: "acme.echo/v1",
				Outputs: []contract.PortDef{{
					Name: "result",
					Type: "port.demo.echo/v1",
					Meta: contract.PortMeta{Reportable: true},
				}},
			}},
		},
	})
	unit, err := catalog.Resolve("acme.echo/v1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !unit.Outputs[0].Metadata().Reportable {
		t.Fatal("reportable metadata was not preserved")
	}
}

func TestCatalogCopiesUnitSlices(t *testing.T) {
	inputs := []contract.PortDef{{Name: "input", Type: "port.demo.input/v1"}}
	outputs := []contract.PortDef{{Name: "output", Type: "port.demo.output/v1"}}
	metrics := []contract.MetricDef{{Name: "metric", Type: "counter"}}
	artifacts := []contract.ArtifactDef{{Name: "artifact", ContentType: "application/json"}}

	catalog := NewCatalog([]Plugin{{
		Name: "plugin",
		Units: []Unit{{
			Kind:      "demo.echo/v1",
			Inputs:    inputs,
			Outputs:   outputs,
			Metrics:   metrics,
			Artifacts: artifacts,
		}},
	}})

	inputs[0].Name = "mutated-input"
	outputs[0].Name = "mutated-output"
	metrics[0].Name = "mutated-metric"
	artifacts[0].Name = "mutated-artifact"

	unit, err := catalog.Resolve("demo.echo/v1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if unit.Inputs[0].Name != "input" || unit.Outputs[0].Name != "output" ||
		unit.Metrics[0].Name != "metric" || unit.Artifacts[0].Name != "artifact" {
		t.Fatalf("catalog retained caller-owned slices: %#v", unit)
	}

	unit.Inputs[0].Name = "resolved-input"
	unit.Outputs[0].Name = "resolved-output"
	unit.Metrics[0].Name = "resolved-metric"
	unit.Artifacts[0].Name = "resolved-artifact"

	unit, err = catalog.Resolve("demo.echo/v1")
	if err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	if unit.Inputs[0].Name != "input" || unit.Outputs[0].Name != "output" ||
		unit.Metrics[0].Name != "metric" || unit.Artifacts[0].Name != "artifact" {
		t.Fatalf("resolved unit mutated catalog state: %#v", unit)
	}

	def := unit.Definition()
	def.Inputs[0].Name = "definition-input"
	def.Outputs[0].Name = "definition-output"
	def.Metrics[0].Name = "definition-metric"
	def.Artifacts[0].Name = "definition-artifact"

	def = unit.Definition()
	if def.Inputs[0].Name != "input" || def.Outputs[0].Name != "output" ||
		def.Metrics[0].Name != "metric" || def.Artifacts[0].Name != "artifact" {
		t.Fatalf("definition mutated unit state: %#v", def)
	}
}
