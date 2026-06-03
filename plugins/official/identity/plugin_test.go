package identity

import "testing"

func TestPluginManifest(t *testing.T) {
	manifest := Plugin()
	if manifest.Name != "wkbench.official.identity" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	requireKinds(t, manifest.Units, []string{
		"identity.pool/v1",
		"identity.person_pairs/v1",
	})
}
