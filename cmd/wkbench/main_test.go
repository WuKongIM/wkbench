package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/kernel"
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

func TestRunCommandWritesReportOnAssertionFailure(t *testing.T) {
	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "scenario.yaml")
	reportDir := filepath.Join(dir, "report")
	scenario := `
version: wkbench/v2
run:
  id: cli-failed-report
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
          value: 1
`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"run", "-scenario", scenarioPath}, &stderr)
	if code != exitRun {
		t.Fatalf("expected exitRun, got %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(reportDir, "report.json"))
	if err != nil {
		t.Fatalf("failed run should still write report.json: %v", err)
	}
	if !strings.Contains(string(data), `"traffic"`) || !strings.Contains(string(data), `"sendack_success_total"`) {
		t.Fatalf("failed report should include completed traffic metrics:\n%s", data)
	}
	if !strings.Contains(stderr.String(), "run failed:") {
		t.Fatalf("expected run failed message, got %q", stderr.String())
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
		"core.fake_message_sender/v1",
		"identity.pool/v1",
		"identity.person_pairs/v1",
		"traffic.send/v1",
		"wukongim.target/v1",
		"wukongim.metrics_collector/v1",
		"wukongim.prepare_tokens/v1",
		"wukongim.prepare_group_channels/v1",
		"wkproto.session_pool/v1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected list-units to include %s, got:\n%s", want, out)
		}
	}
}

func TestValidateCommandAcceptsMetricsCollectorScenario(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: metrics-collector
  duration: 1ms
units:
  target:
    use: wukongim.target
    spec:
      api_addrs: ["http://127.0.0.1:5011"]
      gateway_tcp_addrs: ["127.0.0.1:5111"]
      bench_api_token: ""
      operation_timeout: 5s
      skip_readiness: true
      skip_capabilities: true
  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: 1s
      timeout: 800ms
      path: /metrics
      include: ["wk_.*", "wukongim_.*"]
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"validate", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
}

func TestValidateCommandAcceptsMixedSendRateScenario(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: mixed-send-rate
  duration: 1ms
units:
  identities:
    use: identity.pool
    spec:
      total: 4
      uid_prefix: u
      device_prefix: d
  pairs:
    use: identity.person_pairs
    spec:
      count: 2
      mode: ring
  sender:
    use: core.fake_message_sender
  person_traffic:
    use: traffic.send
    inputs:
      targets: pairs.targets
      sender: sender.sender
    spec:
      rate: 1000/s
      payload_size: 8
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"validate", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
}

func TestExplainCommandPrintsScenarioGraph(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-explain
  duration: 1s
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
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"explain", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"Run: cli-explain",
		"Execution Order:",
		"1. groups",
		"2. sender",
		"3. traffic",
		"Wiring:",
		"traffic.channels <- groups.groups",
		"traffic.sender <- sender.sender",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected explain output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestExplainCommandPrintsJSON(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-explain-json
  duration: 1s
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
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"explain", "-scenario", scenarioPath, "-format", "json"}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	var explanation kernel.Explanation
	if err := json.Unmarshal(stderr.Bytes(), &explanation); err != nil {
		t.Fatalf("unmarshal explanation: %v\n%s", err, stderr.String())
	}
	if explanation.RunID != "cli-explain-json" {
		t.Fatalf("unexpected run id %q", explanation.RunID)
	}
	if strings.Join(explanation.Order, ",") != "groups,sender,traffic" {
		t.Fatalf("unexpected order: %#v", explanation.Order)
	}
	if len(explanation.Wiring) != 2 {
		t.Fatalf("unexpected wiring: %#v", explanation.Wiring)
	}
	binding := explanation.Wiring[1]
	if binding.Unit != "traffic" || binding.Input != "sender" || binding.SourceUnit != "sender" || binding.SourceOutput != "sender" {
		t.Fatalf("unexpected sender binding: %#v", binding)
	}
}

func TestPlanCommandPrintsScenarioPlan(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-plan
  duration: 1s
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
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plan", "-scenario", scenarioPath}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"Run: cli-plan",
		"Execution Order:",
		"Plans:",
		"traffic: traffic.group_send/v1",
		"status: completed",
		"shards: 1",
		"Wiring:",
		"traffic.channels <- groups.groups",
		"traffic.sender <- sender.sender",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected plan output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestPlanCommandPrintsJSON(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-plan-json
  duration: 1s
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
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plan", "-scenario", scenarioPath, "-format", "json"}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	var result kernel.PlanResult
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal plan result: %v\n%s", err, stderr.String())
	}
	if result.RunID != "cli-plan-json" || result.Status != kernel.StatusCompleted {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Join(result.Order, ",") != "groups,sender,traffic" {
		t.Fatalf("unexpected order: %#v", result.Order)
	}
	if result.Units["traffic"].Kind != "traffic.group_send/v1" {
		t.Fatalf("unexpected traffic unit: %#v", result.Units["traffic"])
	}
	if len(result.Units["traffic"].Plan.Shards) != 1 {
		t.Fatalf("unexpected traffic plan: %#v", result.Units["traffic"].Plan)
	}
}

func TestPlanCommandRejectsUnsupportedFormat(t *testing.T) {
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: cli-plan-format
units:
  groups:
    use: core.static_groups
    spec:
      count: 1
      members_per_channel: 2
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plan", "-scenario", scenarioPath, "-format", "yaml"}, &stderr)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unsupported plan format") {
		t.Fatalf("expected unsupported format error, got %q", stderr.String())
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

func writeScenarioFile(t *testing.T, content string) string {
	t.Helper()
	scenarioPath := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(scenarioPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return scenarioPath
}
