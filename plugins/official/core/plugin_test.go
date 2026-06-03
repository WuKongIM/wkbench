package core

import "testing"

func TestPluginManifest(t *testing.T) {
	manifest := Plugin()
	if manifest.Name != "wkbench.official.core" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	requireKinds(t, manifest.Units, []string{"core.static_groups/v1"})
}
