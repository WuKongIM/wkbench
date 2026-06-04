package wukongim

import "testing"

func TestPluginManifest(t *testing.T) {
	manifest := Plugin()
	if manifest.Name != "wkbench.official.wukongim" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	requireKinds(t, manifest.Units, []string{
		"wukongim.target/v1",
		"wukongim.prepare_group_channels/v1",
		"wukongim.metrics_collector/v1",
	})
}
