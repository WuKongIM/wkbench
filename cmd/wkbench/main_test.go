package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommandExecutesScenarioAndWritesReport(t *testing.T) {
	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "scenario.yaml")
	reportDir := filepath.Join(dir, "report")
	scenario := `
version: wkbench/v2
run:
  id: cli-demo
  duration: 1s
  report_dir: ` + reportDir + `
units:
  groups:
    use: core.static_groups
    spec:
      count: 1
      members_per_channel: 2
  sender:
    use: core.fake_group_sender
  traffic:
    use: traffic.group_send
    spec:
      rate: 2/s
      payload_size: 16
  limits:
    use: report.assert
    inputs:
      summary: traffic.summary
    spec:
      rules:
        - metric: sendack_error_rate
          op: eq
          value: 0
`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"run", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(reportDir, "report.json")); err != nil {
		t.Fatalf("report not written: %v", err)
	}
	if !strings.Contains(stderr.String(), "wkbench run completed") {
		t.Fatalf("expected completion message, got %q", stderr.String())
	}
}

func TestListUnitsIncludesWuKongIMBlackBoxUnits(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"list-units"}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"identity.pool/v1",
		"wukongim.target/v1",
		"wukongim.prepare_tokens/v1",
		"wukongim.prepare_group_channels/v1",
		"wkproto.session_pool/v1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected list-units to include %s, got:\n%s", want, out)
		}
	}
}

func TestNewUnitCommandScaffoldsUnit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "units", "custom", "echo")

	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"new-unit",
		"-kind", "custom.echo/v1",
		"-dir", dir,
		"-title", "Echo unit",
		"-description", "Echoes deterministic inputs for tests.",
	}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	for _, name := range []string{"unit.go", "unit_test.go", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s not generated: %v", name, err)
		}
	}
	unitGo, err := os.ReadFile(filepath.Join(dir, "unit.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unitGo), `const kind = "custom.echo/v1"`) {
		t.Fatalf("unit.go missing kind:\n%s", unitGo)
	}
	if !strings.Contains(stderr.String(), "created unit custom.echo/v1") {
		t.Fatalf("expected created message, got %q", stderr.String())
	}
}

func TestNewUnitCommandRejectsOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unit.go"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"new-unit", "-kind", "custom.echo/v1", "-dir", dir}, &stderr)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d", code)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("expected overwrite error, got %q", stderr.String())
	}
}
