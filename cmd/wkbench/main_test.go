package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/kernel"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wkbench-official-plugins-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create official plugin temp dir: %v\n", err)
		os.Exit(1)
	}
	specs, err := buildOfficialPluginSpecsForTest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build official plugin binaries: %v\n", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	officialPluginSpecsForTest = func() ([]pluginCommandSpec, error) {
		return specs, nil
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

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
		"core.static_groups/v1",
		"core.fake_message_sender/v1",
		"identity.pool/v1",
		"identity.person_pairs/v1",
		"report.assert/v1",
		"traffic.send/v1",
		"wukongim.target/v1",
		"wukongim.metrics_collector/v1",
		"wukongim.prepare_tokens/v1",
		"wukongim.prepare_group_channels/v1",
		"wkbench.official.core:core.static_groups/v1",
		"wkbench.official.identity:identity.pool/v1",
		"wkbench.official.report:report.assert/v1",
		"wkbench.official.wukongim:wukongim.target/v1",
		"wkbench.official.wukongim:wukongim.metrics_collector/v1",
		"wkproto.session_pool/v1",
	} {
		requireOutputLine(t, out, want)
	}
}

func TestNoOfficialPluginsKeepsOnlyHostLocalUnits(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-no-official-plugins", "list-units"}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"core.fake_group_sender/v1",
		"core.fake_message_sender/v1",
		"traffic.group_send/v1",
		"traffic.send/v1",
		"wkproto.session_pool/v1",
		"wukongim.prepare_tokens/v1",
	} {
		requireOutputLine(t, out, want)
	}
	for _, absent := range []string{
		"core.static_groups/v1",
		"identity.pool/v1",
		"identity.person_pairs/v1",
		"report.assert/v1",
		"wukongim.metrics_collector/v1",
		"wukongim.target/v1",
		"wukongim.prepare_group_channels/v1",
	} {
		if outputHasLine(out, absent) {
			t.Fatalf("expected %s to be absent with official plugins disabled, got:\n%s", absent, out)
		}
	}
	if strings.Contains(out, "wkbench.official.") {
		t.Fatalf("expected qualified official units to be absent with official plugins disabled, got:\n%s", out)
	}
}

func TestListUnitsIncludesExternalPlugin(t *testing.T) {
	bin := buildDemoPlugin(t)
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "list-units"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"demo.echo/v1", "wkbench.demo:demo.echo/v1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list-units missing %s:\n%s", want, out)
		}
	}
}

func TestValidateExternalPluginScenario(t *testing.T) {
	bin := buildDemoPlugin(t)
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "validate", "-scenario", filepath.Join(repoRoot(t), "examples/plugin-echo.yaml")}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench scenario is valid") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
}

func TestRunExternalPluginScenario(t *testing.T) {
	bin := buildDemoPlugin(t)
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "run", "-scenario", filepath.Join(repoRoot(t), "examples/plugin-echo.yaml")}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench run completed") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
}

func TestValidateExternalPluginScenarioWithQualifiedKind(t *testing.T) {
	bin := buildDemoPlugin(t)
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: plugin-qualified
  duration: 1s
units:
  echo:
    use: wkbench.demo:demo.echo/v1
    spec:
      message: hello qualified plugin
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "validate", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench scenario is valid") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
}

func TestRunExternalPluginScenarioWithQualifiedKind(t *testing.T) {
	bin := buildDemoPlugin(t)
	reportDir := filepath.Join(t.TempDir(), "report")
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: plugin-qualified
  duration: 1s
  report_dir: `+reportDir+`
units:
  echo:
    use: wkbench.demo:demo.echo/v1
    spec:
      message: hello qualified plugin
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "run", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench run completed") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(reportDir, "artifacts", "echo", "echo.json")); err != nil {
		t.Fatalf("artifact not written: %v", err)
	}
}

func TestRunExternalPluginScenarioWritesReportableOutput(t *testing.T) {
	bin := buildDemoPlugin(t)
	dir := t.TempDir()
	reportDir := filepath.Join(dir, "report")
	scenarioPath := filepath.Join(dir, "plugin-echo.yaml")
	scenario := `
version: wkbench/v2
run:
  id: plugin-echo-report
  duration: 1s
  report_dir: ` + reportDir + `
units:
  echo:
    use: demo.echo/v1
    spec:
      message: hello from plugin report
`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "run", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(reportDir, "report.json"))
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	if !strings.Contains(string(data), "hello from plugin report") {
		t.Fatalf("report.json missing remote output value:\n%s", data)
	}
}

func TestRunOfficialDataPluginOutputIntoLocalGroupSend(t *testing.T) {
	bin := buildOfficialDataPlugin(t)
	dir := t.TempDir()
	reportDir := filepath.Join(dir, "report")
	scenarioPath := filepath.Join(dir, "mixed.yaml")
	scenario := `
version: wkbench/v2
run:
  id: official-data-to-local
  duration: 1s
  report_dir: ` + reportDir + `
units:
  groups:
    use: wkbench.official.data:core.static_groups/v1
    spec:
      count: 1
      members_per_channel: 2
  sender:
    use: core.fake_group_sender
  traffic:
    use: traffic.group_send
    inputs:
      channels: groups.groups
      sender: sender.sender
    spec:
      rate: 2/s
      payload_size: 16
`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "run", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench run completed") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
}

func TestListUnitsClosesExternalPluginClient(t *testing.T) {
	bin, closedPath := buildTrackingDemoPlugin(t)
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "list-units"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	waitForFile(t, closedPath)
}

func TestPluginRegistrationDuplicateBareKindKeepsQualifiedReferences(t *testing.T) {
	firstBin, firstClosed := buildNamedTrackingDemoPlugin(t, "wkbench.demo.first")
	secondBin, secondClosed := buildNamedTrackingDemoPlugin(t, "wkbench.demo.second")
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: plugin-qualified-duplicate
  duration: 1s
units:
  echo:
    use: wkbench.demo.second:demo.echo/v1
    spec:
      message: hello duplicate plugin
`)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", firstBin, "-plugin", secondBin, "validate", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("qualified code = %d, stderr:\n%s", code, stderr.String())
	}
	waitForFile(t, firstClosed)
	waitForFile(t, secondClosed)

	firstBin, firstClosed = buildNamedTrackingDemoPlugin(t, "wkbench.demo.first")
	secondBin, secondClosed = buildNamedTrackingDemoPlugin(t, "wkbench.demo.second")
	bareScenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: plugin-bare-duplicate
  duration: 1s
units:
  echo:
    use: demo.echo/v1
    spec:
      message: hello ambiguous plugin
`)

	stderr.Reset()
	code = runWithStderr([]string{"-plugin", firstBin, "-plugin", secondBin, "validate", "-scenario", bareScenarioPath}, &stderr)
	if code != exitConfig {
		t.Fatalf("bare code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `unit kind "demo.echo/v1" is not registered`) {
		t.Fatalf("bare use was not rejected as unavailable:\n%s", stderr.String())
	}
	waitForFile(t, firstClosed)
	waitForFile(t, secondClosed)
}

func TestDefaultOfficialBareKindSurvivesUserPluginDuplicate(t *testing.T) {
	bin, closed := buildNamedStaticGroupsPlugin(t, "wkbench.acme.static")
	var listStderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "list-units"}, &listStderr)
	if code != exitOK {
		t.Fatalf("list-units code = %d, stderr:\n%s", code, listStderr.String())
	}
	requireOutputLine(t, listStderr.String(), "core.static_groups/v1")
	requireOutputLine(t, listStderr.String(), "wkbench.official.core:core.static_groups/v1")
	requireOutputLine(t, listStderr.String(), "wkbench.acme.static:core.static_groups/v1")
	waitForFile(t, closed)

	bin, closed = buildNamedStaticGroupsPlugin(t, "wkbench.acme.static")
	scenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: official-bare-wins
  duration: 1s
units:
  groups:
    use: core.static_groups/v1
    spec:
      count: 1
      members_per_channel: 2
  sender:
    use: core.fake_group_sender
  traffic:
    use: traffic.group_send
    inputs:
      channels: groups.groups
      sender: sender.sender
    spec:
      rate: 1/s
      payload_size: 8
`)

	var stderr bytes.Buffer
	code = runWithStderr([]string{"-plugin", bin, "validate", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	waitForFile(t, closed)

	bin, closed = buildNamedStaticGroupsPlugin(t, "wkbench.acme.static")
	qualifiedScenarioPath := writeScenarioFile(t, `
version: wkbench/v2
run:
  id: user-qualified-still-works
  duration: 1s
units:
  groups:
    use: wkbench.acme.static:core.static_groups/v1
    spec:
      count: 1
      members_per_channel: 2
  sender:
    use: core.fake_group_sender
  traffic:
    use: traffic.group_send
    inputs:
      channels: groups.groups
      sender: sender.sender
    spec:
      rate: 1/s
      payload_size: 8
`)

	stderr.Reset()
	code = runWithStderr([]string{"-plugin", bin, "validate", "-scenario", qualifiedScenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("qualified code = %d, stderr:\n%s", code, stderr.String())
	}
	waitForFile(t, closed)
}

func TestValidateLoadsPluginsFromProjectConfig(t *testing.T) {
	bin := buildDemoPlugin(t)
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "demo", bin)
	scenarioPath := filepath.Join(projectDir, "scenario.yaml")
	scenario := `
version: wkbench/v2
run:
  id: configured-plugin
  duration: 1s
units:
  echo:
    use: demo.echo/v1
    spec:
      message: hello configured plugin
`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"validate", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench scenario is valid") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
}

func TestListUnitsLoadsPluginsFromProjectConfig(t *testing.T) {
	bin := buildDemoPlugin(t)
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "demo", bin)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"list-units"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"demo.echo/v1", "wkbench.demo:demo.echo/v1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list-units missing %s:\n%s", want, out)
		}
	}
}

func TestConfiguredPluginPathDedupesGlobalPluginFlag(t *testing.T) {
	bin := buildDemoPlugin(t)
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "demo", bin)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"-plugin", bin, "list-units"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "registration failed") {
		t.Fatalf("duplicate plugin path should not double-register:\n%s", stderr.String())
	}
}

func TestPluginListPrintsConfiguredPlugins(t *testing.T) {
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "demo", "./bin/wkbench-demo-plugin")

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "list"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"demo", "./bin/wkbench-demo-plugin"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plugin list missing %q:\n%s", want, out)
		}
	}
}

func TestPluginDoctorHandshakesConfiguredPlugins(t *testing.T) {
	bin := buildDemoPlugin(t)
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "demo", bin)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "doctor"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"demo", "ok", "wkbench.demo", "demo.echo/v1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plugin doctor missing %q:\n%s", want, out)
		}
	}
}

func TestPluginDoctorTimesOutHungPlugin(t *testing.T) {
	bin := buildHungPlugin(t)
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "hung", bin)

	var stderr bytes.Buffer
	start := time.Now()
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "doctor"}, &stderr)
	elapsed := time.Since(start)
	if code != exitConfig {
		t.Fatalf("expected exitConfig for hung plugin, got %d stderr:\n%s", code, stderr.String())
	}
	if elapsed > 3*time.Second {
		t.Fatalf("doctor should time out hung plugin quickly, elapsed=%s stderr:\n%s", elapsed, stderr.String())
	}
	if !strings.Contains(stderr.String(), "hung failed") {
		t.Fatalf("expected hung plugin failure report, got:\n%s", stderr.String())
	}
}

func TestPluginInspectPrintsManifestForConfiguredPlugin(t *testing.T) {
	bin := buildDemoPlugin(t)
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "demo", bin)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "inspect", "demo"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"Plugin: wkbench.demo", "Version: 0.1.0", "Units:", "demo.echo/v1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plugin inspect missing %q:\n%s", want, out)
		}
	}
}

func TestPluginAddFromSubdirStoresPathRelativeToProjectConfig(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".wkbench")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "plugins.yaml"), []byte("version: wkbench.plugins/v1\nplugins: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(projectDir, "tools")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, subdir, []string{"plugin", "add", "demo", "./bin/acme-plugin"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(configDir, "plugins.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "path: tools/bin/acme-plugin") {
		t.Fatalf("plugin add from subdir should store path relative to project config:\n%s", data)
	}
}

func TestPluginAddWritesProjectConfig(t *testing.T) {
	projectDir := t.TempDir()
	pluginPath := filepath.Join(projectDir, "bin", "wkbench-demo-plugin")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginPath, []byte("placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "add", "demo", "./bin/wkbench-demo-plugin"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(projectDir, ".wkbench", "plugins.yaml"))
	if err != nil {
		t.Fatalf("plugins.yaml not written: %v", err)
	}
	for _, want := range []string{"plugins:", "name: demo", "path: bin/wkbench-demo-plugin"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("plugins.yaml missing %q:\n%s", want, data)
		}
	}
}

func TestPluginManagementCommandsCanRepairMissingPluginPath(t *testing.T) {
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "missing", "./bin/missing-plugin")

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"validate", "-scenario", writeScenarioFile(t, `
version: wkbench/v2
run:
  id: bad-plugin-config
units:
  echo:
    use: demo.echo/v1
    spec:
      message: hello
`)}, &stderr)
	if code != exitConfig {
		t.Fatalf("validate should fail when configured plugin path is missing, code = %d stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "failed to start") {
		t.Fatalf("validate should report plugin start failure:\n%s", stderr.String())
	}

	stderr.Reset()
	code = runInDirWithStderr(t, projectDir, []string{"plugin", "add", "fixed", "./bin/fixed-plugin"}, &stderr)
	if code != exitOK {
		t.Fatalf("plugin add should still work with missing configured plugin path, code = %d stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "configured plugin fixed") {
		t.Fatalf("plugin add did not report success:\n%s", stderr.String())
	}

	initDir := filepath.Join(projectDir, "generated")
	stderr.Reset()
	code = runInDirWithStderr(t, projectDir, []string{
		"plugin", "init",
		"-dir", initDir,
		"-module", "example.com/fixed/plugin",
		"-name", "fixed.echo",
	}, &stderr)
	if code != exitOK {
		t.Fatalf("plugin init should still work with missing configured plugin path, code = %d stderr:\n%s", code, stderr.String())
	}
}

func TestNewUnitCommandIgnoresMissingConfiguredPluginPath(t *testing.T) {
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "missing", "./bin/missing-plugin")
	unitDir := filepath.Join(projectDir, "units", "custom", "echo")

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{
		"new-unit",
		"-kind", "custom.echo/v1",
		"-dir", unitDir,
	}, &stderr)
	if code != exitOK {
		t.Fatalf("new-unit should not start configured plugins, code = %d stderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(unitDir, "unit.go")); err != nil {
		t.Fatalf("unit.go not generated: %v", err)
	}
}

func TestPluginInitGeneratesBuildableExternalPlugin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-plugin")

	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"plugin", "init",
		"-dir", dir,
		"-module", "example.com/acme/wkbench-plugin",
		"-name", "acme.echo",
	}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	for _, path := range []string{
		"go.mod",
		filepath.Join("cmd", "acme-echo-plugin", "main.go"),
		filepath.Join("units", "echo", "unit.go"),
		filepath.Join("examples", "echo.yaml"),
		"README.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatalf("%s not generated: %v", path, err)
		}
	}
	testCmd := exec.Command("go", "test", "./...")
	testCmd.Dir = dir
	testCmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := testCmd.CombinedOutput(); err != nil {
		t.Fatalf("generated plugin tests do not pass: %v\n%s", err, out)
	}
	bin := filepath.Join(dir, "bin", "acme-echo-plugin")
	build := exec.Command("go", "build", "./cmd/acme-echo-plugin")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("generated plugin does not build: %v\n%s", err, out)
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	build = exec.Command("go", "build", "-o", bin, "./cmd/acme-echo-plugin")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("generated plugin binary does not build: %v\n%s", err, out)
	}
	var validateStderr bytes.Buffer
	code = runWithStderr([]string{"-plugin", bin, "validate", "-scenario", filepath.Join(dir, "examples", "echo.yaml")}, &validateStderr)
	if code != exitOK {
		t.Fatalf("generated plugin scenario does not validate, code = %d stderr:\n%s", code, validateStderr.String())
	}
}

func buildDemoPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "wkbench-demo-plugin")
	cmd := exec.Command("go", "build", "-o", bin, "./plugins/demo/cmd/wkbench-demo-plugin")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build demo plugin: %v\n%s", err, out)
	}
	return bin
}

func buildOfficialDataPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "wkbench-official-data-plugin")
	cmd := exec.Command("go", "build", "-o", bin, "./plugins/official/dataplane/cmd/wkbench-official-data-plugin")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build official data plugin: %v\n%s", err, out)
	}
	return bin
}

func buildOfficialPluginSpecsForTest(dir string) ([]pluginCommandSpec, error) {
	plugins := []struct {
		name string
		pkg  string
		bin  string
	}{
		{name: "wkbench.official.core", pkg: "./plugins/official/core/cmd/wkbench-official-core-plugin", bin: "wkbench-official-core-plugin"},
		{name: "wkbench.official.identity", pkg: "./plugins/official/identity/cmd/wkbench-official-identity-plugin", bin: "wkbench-official-identity-plugin"},
		{name: "wkbench.official.wukongim", pkg: "./plugins/official/wukongim/cmd/wkbench-official-wukongim-plugin", bin: "wkbench-official-wukongim-plugin"},
		{name: "wkbench.official.report", pkg: "./plugins/official/report/cmd/wkbench-official-report-plugin", bin: "wkbench-official-report-plugin"},
	}
	specs := make([]pluginCommandSpec, 0, len(plugins))
	for _, plugin := range plugins {
		bin := filepath.Join(dir, plugin.bin)
		cmd := exec.Command("go", "build", "-o", bin, plugin.pkg)
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("%s: %w\n%s", plugin.name, err, out)
		}
		specs = append(specs, pluginCommandSpec{Label: plugin.name, Path: bin})
	}
	return specs, nil
}

func buildTrackingDemoPlugin(t *testing.T) (string, string) {
	t.Helper()
	return buildNamedTrackingDemoPlugin(t, "wkbench.demo.tracking")
}

func buildNamedTrackingDemoPlugin(t *testing.T, pluginName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	closedPath := filepath.Join(dir, "closed")
	sourcePath := filepath.Join(dir, "main.go")
	source := fmt.Sprintf(`package main

import (
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/plugins/demo/echo"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	defer os.WriteFile(%q, []byte("closed"), 0o644)
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name:    %q,
		Version: "0.1.0",
		Units:   []contract.Unit{echo.Unit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`, closedPath, pluginName)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "wkbench-tracking-plugin")
	cmd := exec.Command("go", "build", "-o", bin, sourcePath)
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tracking plugin: %v\n%s", err, out)
	}
	return bin, closedPath
}

func buildNamedStaticGroupsPlugin(t *testing.T, pluginName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	closedPath := filepath.Join(dir, "closed")
	sourcePath := filepath.Join(dir, "main.go")
	source := fmt.Sprintf(`package main

import (
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	defer os.WriteFile(%q, []byte("closed"), 0o644)
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name:    %q,
		Version: "0.1.0",
		Units:   []contract.Unit{staticgroups.Unit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`, closedPath, pluginName)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "wkbench-static-groups-plugin")
	cmd := exec.Command("go", "build", "-o", bin, sourcePath)
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build static groups plugin: %v\n%s", err, out)
	}
	return bin, closedPath
}

func buildHungPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	source := `package main

import "time"

func main() {
	time.Sleep(5 * time.Second)
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "wkbench-hung-plugin")
	cmd := exec.Command("go", "build", "-o", bin, sourcePath)
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build hung plugin: %v\n%s", err, out)
	}
	return bin
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
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

func TestRunRemoteMetricsCollectorWithLocalMetricsServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, "wk_active_conn_count 7\n")
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "remote-metrics.yaml")
	reportDir := filepath.Join(dir, "report")
	scenario := fmt.Sprintf(`
version: wkbench/v2
run:
  id: remote-metrics-smoke
  duration: 120ms
  report_dir: %q
units:
  target:
    use: wukongim.target
    spec:
      api_addrs: [%q]
      gateway_tcp_addrs: ["127.0.0.1:0"]
      bench_api_token: ""
      operation_timeout: 50ms
      skip_readiness: true
      skip_capabilities: true
  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: 20ms
      timeout: 50ms
      path: /metrics
      include: ["wk_active_conn_count"]
  identities:
    use: identity.pool
    spec:
      total: 2
      uid_prefix: u
      device_prefix: d
  pairs:
    use: identity.person_pairs
    inputs:
      identities: identities.pool
    spec:
      count: 1
      mode: ring
  sender:
    use: core.fake_message_sender
  traffic:
    use: traffic.send
    after: [metrics]
    inputs:
      targets: pairs.targets
      sender: sender.sender
    spec:
      rate: 20/s
      payload_size: 8
`, reportDir, server.URL)
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"run", "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("run exit code = %d, stderr:\n%s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(reportDir, "artifacts", "metrics", "metrics.jsonl"))
	if err != nil {
		t.Fatalf("read metrics artifact: %v", err)
	}
	if !bytes.Contains(data, []byte("wk_active_conn_count")) {
		t.Fatalf("metrics artifact missing scraped metric:\n%s", string(data))
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

func TestExampleScenariosValidateExplainPlanWithDefaultOfficialPlugins(t *testing.T) {
	root := repoRoot(t)
	scenarios := []string{
		"examples/group-send.yaml",
		"examples/wukongim-group-send.yaml",
		"examples/wukongim-send-rate-mixed.yaml",
		"examples/wukongim-send-rate-with-metrics.yaml",
		"examples/wukongim-three-node-send-rate-mixed.yaml",
	}
	for _, scenario := range scenarios {
		for _, command := range []string{"validate", "explain", "plan"} {
			t.Run(command+"/"+scenario, func(t *testing.T) {
				var stderr bytes.Buffer
				code := runWithStderr([]string{command, "-scenario", filepath.Join(root, scenario)}, &stderr)
				if code != exitOK {
					t.Fatalf("%s %s code = %d, stderr:\n%s", command, scenario, code, stderr.String())
				}
			})
		}
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

func requireOutputLine(t *testing.T, out, want string) {
	t.Helper()
	if !outputHasLine(out, want) {
		t.Fatalf("expected output line %s, got:\n%s", want, out)
	}
}

func outputHasLine(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func runInDirWithStderr(t *testing.T, dir string, args []string, stderr *bytes.Buffer) int {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	}()
	return runWithStderr(args, stderr)
}

func writePluginConfig(t *testing.T, projectDir, name, path string) {
	t.Helper()
	configDir := filepath.Join(projectDir, ".wkbench")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("plugins:\n  - name: %s\n    path: %s\n", name, path)
	if err := os.WriteFile(filepath.Join(configDir, "plugins.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}
