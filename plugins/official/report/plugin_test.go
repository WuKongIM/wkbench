package report

import "testing"

func TestPluginManifest(t *testing.T) {
	manifest := Plugin()
	if manifest.Name != "wkbench.official.report" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	requireKinds(t, manifest.Units, []string{"report.assert/v1"})
}
