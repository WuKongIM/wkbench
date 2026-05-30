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
