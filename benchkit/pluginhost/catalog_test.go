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
