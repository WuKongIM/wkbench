package dataplane_test

import (
	"testing"

	officialdata "github.com/WuKongIM/wkbench/plugins/official/dataplane"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func TestOfficialDataManifestContainsPureDataUnits(t *testing.T) {
	source := officialdata.Plugin()
	manifest := plugin.ManifestFromUnits(source.Name, source.Version, source.Units)
	kinds := map[string]bool{}
	for _, unit := range manifest.Units {
		kinds[unit.Kind] = true
	}
	for _, kind := range []string{"identity.pool/v1", "identity.person_pairs/v1", "core.static_groups/v1", "report.assert/v1"} {
		if !kinds[kind] {
			t.Fatalf("manifest missing %s", kind)
		}
	}
}
