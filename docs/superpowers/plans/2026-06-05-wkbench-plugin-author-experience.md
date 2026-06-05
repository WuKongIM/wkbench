# wkbench Plugin Author Experience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class `wkbench plugin check` acceptance command and update generated plugin projects/docs so external plugin authors can verify a binary before sharing or configuring it.

**Architecture:** Keep author checks in `cmd/wkbench`, next to existing plugin management commands. Add a focused `plugin_check.go` for check parsing, manifest validation, report writing, and optional scenario validation while reusing the existing stdio plugin host and kernel registry paths.

**Tech Stack:** Go standard library, existing `benchkit/pluginhost`, `benchkit/contract`, `benchkit/kernel`, existing CLI test helpers in `cmd/wkbench/main_test.go`, Markdown docs.

---

## File Structure

- Create `cmd/wkbench/plugin_check.go`
  - Owns `runPluginCheck`, argument parsing, target resolution, manifest validation, check reporting, and scenario check helpers.
- Modify `cmd/wkbench/plugin_commands.go`
  - Adds `check` to `wkbench plugin <...>` dispatch and usage.
  - Changes `inspectPluginManifest` to call a timeout-aware helper.
- Modify `cmd/wkbench/main.go`
  - Adds optional per-plugin handshake timeout support for scenario-mode
    plugin checks without changing normal command behavior.
- Modify `cmd/wkbench/plugin_init.go`
  - Adds `scripts/check.sh` to generated projects.
  - Writes generated scripts with executable mode.
  - Expands generated README content with the author acceptance loop.
- Modify `cmd/wkbench/main_test.go`
  - Adds CLI coverage for `plugin check`.
  - Extends generated plugin tests to include `scripts/check.sh`.
- Modify `docs/plugin-authoring.md`
  - Documents the author journey and the distinction between `plugin check`, `plugin doctor`, and `plugin inspect`.

---

### Task 1: Manifest Validation Helpers

**Files:**
- Create: `cmd/wkbench/plugin_check.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write failing manifest validation tests**

Append these tests and helpers near the existing plugin management tests in `cmd/wkbench/main_test.go`:

```go
func TestPluginCheckReportsInvalidManifest(t *testing.T) {
	bin := buildInvalidManifestPlugin(t)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plugin", "check", bin}, &stderr)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d stderr:\n%s", code, stderr.String())
	}
	for _, want := range []string{
		"Plugin check: failed",
		"unit kind \"bad.echo\" must end with /vN",
		"unit kind \"bad.echo\" is declared more than once",
		"output secret is sensitive; sensitive inline data ports cannot cross plugin RPC in Phase 1",
		"artifact name is required",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("plugin check output missing %q:\n%s", want, stderr.String())
		}
	}
}

func buildInvalidManifestPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	source := `
package main

import (
	"context"
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

type badUnit struct{}

func (badUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "bad.echo",
		Outputs: []contract.PortDef{
			{
				Name: "secret",
				Type: "port.bad.secret/v1",
				Meta: contract.PortMeta{
					Boundary: contract.PortBoundaryData,
					Transport: contract.PortTransportInline,
					Sensitive: true,
				},
			},
		},
		Artifacts: []contract.ArtifactDef{{}},
	}
}

func (badUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (badUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (badUnit) Run(context.Context, contract.RunEnv) error { return nil }

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name: "bad.plugin",
		Version: "0.1.0",
		Units: []contract.Unit{badUnit{}, badUnit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bad-plugin")
	cmd := exec.Command("go", "build", "-o", bin, sourcePath)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build invalid manifest plugin: %v\n%s", err, out)
	}
	return bin
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run TestPluginCheckReportsInvalidManifest -count=1
```

Expected: FAIL with `unknown plugin command "check"`.

- [ ] **Step 3: Create manifest validation implementation**

Create `cmd/wkbench/plugin_check.go` with this content:

```go
package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
)

const pluginProtocolV1 = "wkbench.plugin/v1"

type pluginCheckOptions struct {
	Target       string
	ScenarioPath string
	Timeout      time.Duration
}

type pluginCheckTarget struct {
	Label string
	Path  string
}

type pluginCheckIssue struct {
	Subject string
	Message string
}

func validatePluginCheckManifest(manifest pluginhost.Plugin) []pluginCheckIssue {
	var issues []pluginCheckIssue
	if strings.TrimSpace(manifest.Name) == "" {
		issues = append(issues, pluginCheckIssue{Subject: "plugin", Message: "plugin name is required"})
	}
	if manifest.Protocol != pluginProtocolV1 {
		issues = append(issues, pluginCheckIssue{
			Subject: "plugin",
			Message: fmt.Sprintf("protocol %q is not compatible with %s", manifest.Protocol, pluginProtocolV1),
		})
	}
	seenKinds := make(map[string]struct{}, len(manifest.Units))
	for _, unit := range manifest.Units {
		subject := unit.Kind
		if subject == "" {
			subject = "unit"
		}
		if strings.TrimSpace(unit.Kind) == "" {
			issues = append(issues, pluginCheckIssue{Subject: subject, Message: "unit kind is required"})
		} else {
			if !hasVersionSuffixForCheck(unit.Kind) {
				issues = append(issues, pluginCheckIssue{
					Subject: subject,
					Message: fmt.Sprintf("unit kind %q must end with /vN", unit.Kind),
				})
			}
			if _, ok := seenKinds[unit.Kind]; ok {
				issues = append(issues, pluginCheckIssue{
					Subject: subject,
					Message: fmt.Sprintf("unit kind %q is declared more than once", unit.Kind),
				})
			}
			seenKinds[unit.Kind] = struct{}{}
		}
		issues = append(issues, validatePluginCheckPorts(subject, "input", unit.Inputs)...)
		issues = append(issues, validatePluginCheckPorts(subject, "output", unit.Outputs)...)
		for _, artifact := range unit.Artifacts {
			if strings.TrimSpace(artifact.Name) == "" {
				issues = append(issues, pluginCheckIssue{Subject: subject, Message: "artifact name is required"})
			}
		}
	}
	return issues
}

func validatePluginCheckPorts(unitKind, direction string, ports []contract.PortDef) []pluginCheckIssue {
	var issues []pluginCheckIssue
	for _, port := range ports {
		portName := strings.TrimSpace(port.Name)
		if portName == "" {
			issues = append(issues, pluginCheckIssue{Subject: unitKind, Message: direction + " port name is required"})
			portName = direction
		}
		if strings.TrimSpace(string(port.Type)) == "" {
			issues = append(issues, pluginCheckIssue{
				Subject: unitKind,
				Message: fmt.Sprintf("%s %s type is required", direction, portName),
			})
		}
		meta := port.Metadata()
		if meta.Boundary != contract.PortBoundaryData {
			issues = append(issues, pluginCheckIssue{
				Subject: unitKind,
				Message: fmt.Sprintf("%s %s boundary %q cannot cross plugin RPC in Phase 1", direction, portName, meta.Boundary),
			})
		}
		if meta.Transport != contract.PortTransportInline {
			issues = append(issues, pluginCheckIssue{
				Subject: unitKind,
				Message: fmt.Sprintf("%s %s transport %q cannot cross plugin RPC in Phase 1", direction, portName, meta.Transport),
			})
		}
		if meta.Sensitive {
			issues = append(issues, pluginCheckIssue{
				Subject: unitKind,
				Message: fmt.Sprintf("%s %s is sensitive; sensitive inline data ports cannot cross plugin RPC in Phase 1", direction, portName),
			})
		}
		if port.Meta.MaxPayloadBytes < 0 {
			issues = append(issues, pluginCheckIssue{
				Subject: unitKind,
				Message: fmt.Sprintf("%s %s max payload bytes must be positive", direction, portName),
			})
		}
		if len(meta.Encodings) > 0 && !encodingsAllowJSON(meta.Encodings) {
			issues = append(issues, pluginCheckIssue{
				Subject: unitKind,
				Message: fmt.Sprintf("%s %s encodings must include json for Phase 1 inline transport", direction, portName),
			})
		}
	}
	return issues
}

func hasVersionSuffixForCheck(kind string) bool {
	idx := strings.LastIndex(kind, "/v")
	if idx < 0 || idx+2 >= len(kind) {
		return false
	}
	for _, r := range kind[idx+2:] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func encodingsAllowJSON(encodings []string) bool {
	for _, encoding := range encodings {
		if strings.EqualFold(strings.TrimSpace(encoding), "json") {
			return true
		}
	}
	return false
}

func writePluginCheckReport(w io.Writer, manifest pluginhost.Plugin, issues []pluginCheckIssue) {
	if len(issues) == 0 {
		fmt.Fprintln(w, "Plugin check: ok")
	} else {
		fmt.Fprintln(w, "Plugin check: failed")
	}
	fmt.Fprintf(w, "Plugin: %s\n", manifest.Name)
	fmt.Fprintf(w, "Version: %s\n", manifest.Version)
	fmt.Fprintf(w, "Protocol: %s\n", manifest.Protocol)
	if manifest.Source != "" {
		fmt.Fprintf(w, "Source: %s\n", manifest.Source)
	}
	fmt.Fprintln(w, "Units:")
	for _, unit := range manifest.Units {
		status := "ok"
		if len(issuesForSubject(issues, unit.Kind)) > 0 {
			status = "failed"
		}
		background := ""
		if unit.Background {
			background = " background"
		}
		fmt.Fprintf(w, "  - %s %s%s\n", unit.Kind, status, background)
		writePluginCheckPorts(w, "inputs", unit.Inputs)
		writePluginCheckPorts(w, "outputs", unit.Outputs)
		if len(unit.Artifacts) > 0 {
			fmt.Fprintln(w, "    artifacts:")
			for _, artifact := range unit.Artifacts {
				fmt.Fprintf(w, "      - %s\n", artifact.Name)
			}
		}
	}
	if len(issues) == 0 {
		return
	}
	fmt.Fprintln(w, "Issues:")
	for _, issue := range issues {
		fmt.Fprintf(w, "  - %s: %s\n", issue.Subject, issue.Message)
	}
}

func issuesForSubject(issues []pluginCheckIssue, subject string) []pluginCheckIssue {
	var out []pluginCheckIssue
	for _, issue := range issues {
		if issue.Subject == subject {
			out = append(out, issue)
		}
	}
	return out
}

func writePluginCheckPorts(w io.Writer, title string, ports []contract.PortDef) {
	if len(ports) == 0 {
		return
	}
	fmt.Fprintf(w, "    %s:\n", title)
	for _, port := range ports {
		fmt.Fprintf(w, "      - %s %s %s\n", port.Name, port.Type, portCheckSummary(port))
	}
}

func portCheckSummary(port contract.PortDef) string {
	meta := port.Metadata()
	flags := []string{string(meta.Boundary), string(meta.Transport)}
	if meta.Reportable {
		flags = append(flags, "reportable")
	}
	if meta.Sensitive {
		flags = append(flags, "sensitive")
	}
	return strings.Join(flags, " ")
}

func pluginCheckCleanPath(path string) string {
	if path == "" {
		return path
	}
	return filepath.Clean(path)
}
```

This file intentionally includes option and target types now so Task 2 can add
the CLI without changing validation names.

- [ ] **Step 4: Wire the subcommand enough for the test to reach validation**

Modify `cmd/wkbench/plugin_commands.go`:

```go
func runPluginCommand(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench plugin <add|list|doctor|inspect|init|check>")
		return exitConfig
	}
	switch args[0] {
	case "add":
		return runPluginAdd(args[1:], stderr)
	case "list":
		return runPluginList(args[1:], stderr)
	case "doctor":
		return runPluginDoctor(args[1:], stderr)
	case "inspect":
		return runPluginInspect(args[1:], stderr)
	case "init":
		return runPluginInit(args[1:], stderr)
	case "check":
		return runPluginCheck(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "unknown plugin command %q\n", args[0])
		return exitConfig
	}
}
```

Add this temporary `runPluginCheck` to `cmd/wkbench/plugin_check.go`; Task 2
will replace it with full argument parsing:

```go
func runPluginCheck(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: wkbench plugin check <name-or-path> [-scenario path] [-timeout duration]")
		return exitConfig
	}
	path := pluginCheckCleanPath(args[0])
	manifest, err := inspectPluginManifest(path)
	if err != nil {
		fmt.Fprintf(stderr, "plugin check failed: %v\n", err)
		return exitConfig
	}
	issues := validatePluginCheckManifest(manifest)
	writePluginCheckReport(stderr, manifest, issues)
	if len(issues) > 0 {
		return exitConfig
	}
	return exitOK
}
```

- [ ] **Step 5: Run the manifest validation test**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run TestPluginCheckReportsInvalidManifest -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wkbench/plugin_check.go cmd/wkbench/plugin_commands.go cmd/wkbench/main_test.go
git commit -m "feat: validate plugin check manifests"
```

---

### Task 2: `plugin check` CLI, Target Resolution, and Timeout

**Files:**
- Modify: `cmd/wkbench/plugin_check.go`
- Modify: `cmd/wkbench/plugin_commands.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write failing CLI tests**

Append these tests in `cmd/wkbench/main_test.go`:

```go
func TestPluginCheckSucceedsForExternalPlugin(t *testing.T) {
	bin := buildDemoPlugin(t)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plugin", "check", bin}, &stderr)
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d stderr:\n%s", code, stderr.String())
	}
	for _, want := range []string{"Plugin check: ok", "Plugin: wkbench.demo", "demo.echo/v1"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("plugin check output missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestPluginCheckResolvesConfiguredPluginByName(t *testing.T) {
	projectDir := t.TempDir()
	bin := buildDemoPlugin(t)
	writePluginConfig(t, projectDir, "demo", bin)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "check", "demo"}, &stderr)
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Plugin: wkbench.demo") {
		t.Fatalf("configured plugin was not inspected:\n%s", stderr.String())
	}
}

func TestPluginCheckDirectPathIgnoresMissingConfiguredPlugin(t *testing.T) {
	projectDir := t.TempDir()
	writePluginConfig(t, projectDir, "missing", "./bin/missing-plugin")
	bin := buildDemoPlugin(t)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "check", bin}, &stderr)
	if code != exitOK {
		t.Fatalf("direct plugin check should ignore unrelated missing config, code = %d stderr:\n%s", code, stderr.String())
	}
}

func TestPluginCheckDoubleDashTimeoutAfterTarget(t *testing.T) {
	bin := buildDemoPlugin(t)

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plugin", "check", bin, "--timeout", "2s"}, &stderr)
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Plugin: wkbench.demo") {
		t.Fatalf("plugin was not inspected:\n%s", stderr.String())
	}
}

func TestPluginCheckDirectPathIgnoresMalformedConfig(t *testing.T) {
	projectDir := t.TempDir()
	writeMalformedPluginConfig(t, projectDir)
	bin := buildDemoPlugin(t)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "check", bin}, &stderr)
	if code != exitOK {
		t.Fatalf("direct plugin check should ignore malformed config, code = %d stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Plugin: wkbench.demo") {
		t.Fatalf("plugin was not inspected:\n%s", stderr.String())
	}
}

func TestPluginCheckBareTargetReportsMalformedConfig(t *testing.T) {
	projectDir := t.TempDir()
	writeMalformedPluginConfig(t, projectDir)

	var stderr bytes.Buffer
	code := runInDirWithStderr(t, projectDir, []string{"plugin", "check", "demo"}, &stderr)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d stderr:\n%s", code, stderr.String())
	}
	out := stderr.String()
	if !strings.Contains(out, "plugin check failed") {
		t.Fatalf("expected plugin check failure, got:\n%s", out)
	}
	for _, want := range []string{"plugins.yaml", "parse"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected malformed config error containing %q, got:\n%s", want, out)
		}
	}
}

func TestPluginCheckTimesOutHungPlugin(t *testing.T) {
	bin := buildHungPlugin(t)

	var stderr bytes.Buffer
	start := time.Now()
	code := runWithStderr([]string{"plugin", "check", "-timeout", "50ms", bin}, &stderr)
	elapsed := time.Since(start)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d stderr:\n%s", code, stderr.String())
	}
	if elapsed > time.Second {
		t.Fatalf("plugin check should time out promptly, elapsed=%s stderr:\n%s", elapsed, stderr.String())
	}
	if !strings.Contains(stderr.String(), "plugin check failed") {
		t.Fatalf("expected timeout failure, got:\n%s", stderr.String())
	}
}

func TestPluginCheckScenarioPlaceholderDoesNotStartPlugin(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"plugin", "check", "missing-plugin", "--scenario", "./missing.yaml"}, &stderr)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "plugin check -scenario support is added in the scenario validation task") {
		t.Fatalf("expected deferred scenario message, got:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "plugin check failed") {
		t.Fatalf("scenario placeholder should not inspect plugin, got:\n%s", stderr.String())
	}
}

func writeMalformedPluginConfig(t *testing.T, projectDir string) {
	t.Helper()
	configDir := filepath.Join(projectDir, ".wkbench")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "plugins.yaml"), []byte("plugins:\n  - name: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the failing CLI tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestPluginCheck(SucceedsForExternalPlugin|ResolvesConfiguredPluginByName|DirectPathIgnoresMissingConfiguredPlugin|TimesOutHungPlugin|ScenarioPlaceholderDoesNotStartPlugin|DoubleDashTimeoutAfterTarget|DirectPathIgnoresMalformedConfig|BareTargetReportsMalformedConfig)' -count=1
```

Expected: at least the configured-name, timeout, double-dash timeout, and
bare-target malformed config cases fail because Task 1 only accepts exactly one
direct path, has no timeout parsing, and does not distinguish direct paths from
bare configured names.

- [ ] **Step 3: Add robust check argument parsing**

Replace the temporary `runPluginCheck` in `cmd/wkbench/plugin_check.go` with:

```go
func runPluginCheck(args []string, stderr io.Writer) int {
	options, code := parsePluginCheckArgs(args, stderr)
	if code != exitOK {
		return code
	}
	if options.ScenarioPath != "" {
		fmt.Fprintln(stderr, "plugin check -scenario support is added in the scenario validation task")
		return exitConfig
	}
	target, err := resolvePluginCheckTarget(options.Target)
	if err != nil {
		fmt.Fprintf(stderr, "plugin check failed: %v\n", err)
		return exitConfig
	}
	manifest, err := inspectPluginManifestWithTimeout(target.Path, options.Timeout)
	if err != nil {
		fmt.Fprintf(stderr, "plugin check failed: %v\n", err)
		return exitConfig
	}
	if manifest.Source == "" {
		manifest.Source = target.Path
	}
	issues := validatePluginCheckManifest(manifest)
	writePluginCheckReport(stderr, manifest, issues)
	if len(issues) > 0 {
		return exitConfig
	}
	return exitOK
}

func parsePluginCheckArgs(args []string, stderr io.Writer) (pluginCheckOptions, int) {
	options := pluginCheckOptions{Timeout: pluginManifestTimeout}
	fs := flag.NewFlagSet("plugin check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: wkbench plugin check <name-or-path> [-scenario path] [-timeout duration]")
	}
	fs.StringVar(&options.ScenarioPath, "scenario", "", "scenario path")
	fs.DurationVar(&options.Timeout, "timeout", pluginManifestTimeout, "plugin manifest timeout")
	if err := fs.Parse(pluginCheckInterspersedFlagArgs(args)); err != nil {
		return options, exitConfig
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return options, exitConfig
	}
	options.Target = rest[0]
	return options, exitOK
}

func pluginCheckInterspersedFlagArgs(args []string) []string {
	flags := make([]string, 0, len(args))
	targets := make([]string, 0, len(args))
	sawTerminator := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			sawTerminator = true
			targets = append(targets, args[i+1:]...)
			break
		}
		if pluginCheckFlagTakesValue(arg) {
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		if pluginCheckFlagHasInlineValue(arg) {
			flags = append(flags, arg)
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			continue
		}
		targets = append(targets, arg)
	}
	ordered := make([]string, 0, len(args))
	ordered = append(ordered, flags...)
	if sawTerminator {
		ordered = append(ordered, "--")
	}
	ordered = append(ordered, targets...)
	return ordered
}

func pluginCheckFlagTakesValue(arg string) bool {
	return arg == "-scenario" ||
		arg == "--scenario" ||
		arg == "-timeout" ||
		arg == "--timeout"
}

func pluginCheckFlagHasInlineValue(arg string) bool {
	return strings.HasPrefix(arg, "-scenario=") ||
		strings.HasPrefix(arg, "--scenario=") ||
		strings.HasPrefix(arg, "-timeout=") ||
		strings.HasPrefix(arg, "--timeout=")
}
```

Add `flag` to the imports in `cmd/wkbench/plugin_check.go`. The parser must
support `-timeout`, `--timeout`, `-timeout=...`, `--timeout=...`, `-scenario`,
`--scenario`, `-scenario=...`, and `--scenario=...` before or after the
target.

- [ ] **Step 4: Add check target resolution**

Add these helpers to `cmd/wkbench/plugin_check.go`:

```go
func resolvePluginCheckTarget(target string) (pluginCheckTarget, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return pluginCheckTarget{}, fmt.Errorf("plugin target is required")
	}
	if pluginCheckTargetLooksLikePath(target) {
		return pluginCheckTarget{Label: target, Path: filepath.Clean(target)}, nil
	}
	configPath, cfg, ok, err := readProjectPluginConfig()
	if err != nil {
		return pluginCheckTarget{}, err
	}
	if ok {
		projectDir := pluginConfigProjectDir(configPath)
		for _, plugin := range cfg.Plugins {
			if plugin.Name == target {
				return pluginCheckTarget{Label: target, Path: resolvePluginPath(projectDir, plugin.Path)}, nil
			}
		}
	}
	return pluginCheckTarget{Label: target, Path: filepath.Clean(target)}, nil
}

func pluginCheckTargetLooksLikePath(target string) bool {
	return filepath.IsAbs(target) ||
		strings.HasPrefix(target, ".") ||
		strings.Contains(target, string(filepath.Separator))
}
```

Path-like targets return before reading plugin config, so direct checks ignore
unrelated missing or malformed config. Bare-name targets read project config; if
the config exists but cannot be read or parsed, return that error instead of
falling through to PATH. If there is no config, or no matching entry in a
readable config, fall back to `filepath.Clean(target)`.

- [ ] **Step 5: Add timeout-aware inspect helper**

Modify `cmd/wkbench/plugin_commands.go` so `inspectPluginManifest` delegates to
a timeout-aware helper:

```go
func inspectPluginManifest(path string) (pluginhost.Plugin, error) {
	return inspectPluginManifestWithTimeout(path, pluginManifestTimeout)
}

func inspectPluginManifestWithTimeout(path string, timeout time.Duration) (pluginhost.Plugin, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := pluginhost.StartStdioClient(ctx, path)
	if err != nil {
		return pluginhost.Plugin{}, err
	}
	defer client.Close()
	manifest, err := client.Handshake(ctx)
	if err != nil {
		return pluginhost.Plugin{}, err
	}
	if manifest.Source == "" {
		manifest.Source = path
	}
	return manifest, nil
}
```

- [ ] **Step 6: Run CLI tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestPluginCheck(SucceedsForExternalPlugin|ResolvesConfiguredPluginByName|DirectPathIgnoresMissingConfiguredPlugin|TimesOutHungPlugin|ReportsInvalidManifest|ScenarioPlaceholderDoesNotStartPlugin|DoubleDashTimeoutAfterTarget|DirectPathIgnoresMalformedConfig|BareTargetReportsMalformedConfig)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/wkbench/plugin_check.go cmd/wkbench/plugin_commands.go cmd/wkbench/main_test.go
git commit -m "feat: add plugin check command"
```

---

### Task 3: Scenario Validation Mode

**Files:**
- Modify: `cmd/wkbench/plugin_check.go`
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write failing scenario-mode tests**

Append this test in `cmd/wkbench/main_test.go`:

```go
func TestPluginCheckScenarioValidatesExplainsAndPlans(t *testing.T) {
	bin := buildDemoPlugin(t)
	scenarioPath := filepath.Join(repoRoot(t), "examples", "plugin-echo.yaml")

	var stderr bytes.Buffer
	code := runWithStderr([]string{"plugin", "check", bin, "-scenario", scenarioPath}, &stderr)
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d stderr:\n%s", code, stderr.String())
	}
	for _, want := range []string{
		"Scenario: " + scenarioPath,
		"validate: ok",
		"explain: ok",
		"plan: ok",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("plugin check scenario output missing %q:\n%s", want, stderr.String())
		}
	}
}
```

- [ ] **Step 2: Run the failing scenario test**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run TestPluginCheckScenarioValidatesExplainsAndPlans -count=1
```

Expected: FAIL because Task 2 returns an exitConfig message for `-scenario`.

- [ ] **Step 3: Add optional plugin handshake timeout support**

Modify `cmd/wkbench/main.go` so `pluginCommandSpec` can carry a handshake
timeout for author checks. Add `time` to the imports.

```go
type pluginCommandSpec struct {
	Label            string
	Path             string
	Args             []string
	HandshakeTimeout time.Duration
}
```

Inside `loadExternalPlugins`, replace the handshake call:

```go
manifest, err := client.Handshake(context.Background())
```

with:

```go
handshakeCtx := context.Background()
var cancel context.CancelFunc
if spec.HandshakeTimeout > 0 {
	handshakeCtx, cancel = context.WithTimeout(context.Background(), spec.HandshakeTimeout)
}
manifest, err := client.Handshake(handshakeCtx)
if cancel != nil {
	cancel()
}
```

Keep `pluginhost.StartStdioCommand(context.Background(), ...)` unchanged. The
process context must remain uncanceled after a successful handshake so scenario
validation can keep using the plugin process.

- [ ] **Step 4: Implement scenario validation**

First replace the temporary scenario branch in `runPluginCheck`:

```go
if options.ScenarioPath != "" {
	return runPluginCheckScenario(target, options.ScenarioPath, options.Timeout, stderr)
}
```

Then add this function to `cmd/wkbench/plugin_check.go`:

```go
func runPluginCheckScenario(target pluginCheckTarget, scenarioPath string, timeout time.Duration, stderr io.Writer) int {
	fmt.Fprintf(stderr, "Scenario: %s\n", scenarioPath)
	reg := defaultRegistry()
	clients, code := loadExternalPlugins(reg, []pluginCommandSpec{{
		Label: target.Label,
		Path: target.Path,
		HandshakeTimeout: timeout,
	}}, stderr)
	if code != exitOK {
		return code
	}
	defer closePluginClients(clients, stderr)

	scenario, code := loadScenario(scenarioPath, stderr)
	if code != exitOK {
		return code
	}
	engine := kernel.New(reg)
	if err := engine.Validate(context.Background(), scenario); err != nil {
		fmt.Fprintf(stderr, "  validate: failed: %v\n", err)
		return exitConfig
	}
	fmt.Fprintln(stderr, "  validate: ok")
	if _, err := engine.Explain(context.Background(), scenario); err != nil {
		fmt.Fprintf(stderr, "  explain: failed: %v\n", err)
		return exitConfig
	}
	fmt.Fprintln(stderr, "  explain: ok")
	if _, err := engine.Plan(context.Background(), scenario); err != nil {
		fmt.Fprintf(stderr, "  plan: failed: %v\n", err)
		return exitConfig
	}
	fmt.Fprintln(stderr, "  plan: ok")
	return exitOK
}
```

Add `context` and `github.com/WuKongIM/wkbench/benchkit/kernel` to the imports
in `cmd/wkbench/plugin_check.go`.

- [ ] **Step 5: Run scenario-mode tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestPluginCheckScenarioValidatesExplainsAndPlans|TestPluginCheckSucceedsForExternalPlugin' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wkbench/plugin_check.go cmd/wkbench/main.go cmd/wkbench/main_test.go
git commit -m "feat: validate scenarios with plugin check"
```

---

### Task 4: Generated Plugin Check Script and README

**Files:**
- Modify: `cmd/wkbench/plugin_init.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write failing generated-template tests**

In `TestPluginInitGeneratesBuildableExternalPlugin`, extend the expected path
list:

```go
for _, path := range []string{
	"go.mod",
	filepath.Join("cmd", "acme-echo-plugin", "main.go"),
	filepath.Join("units", "echo", "unit.go"),
	filepath.Join("examples", "echo.yaml"),
	filepath.Join("scripts", "check.sh"),
	"README.md",
} {
	if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
		t.Fatalf("%s not generated: %v", path, err)
	}
}
```

Add these assertions after the path checks:

```go
scriptInfo, err := os.Stat(filepath.Join(dir, "scripts", "check.sh"))
	if err != nil {
	t.Fatalf("check script not generated: %v", err)
}
if scriptInfo.Mode()&0o111 == 0 {
	t.Fatalf("check script should be executable, mode=%s", scriptInfo.Mode())
}
readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
if err != nil {
	t.Fatal(err)
}
for _, want := range []string{"wkbench plugin check", "scripts/check.sh", "Phase 1"} {
	if !strings.Contains(string(readme), want) {
		t.Fatalf("generated README missing %q:\n%s", want, readme)
	}
}
```

Add this script execution check after building the generated plugin binary:

```go
wkbenchBin := filepath.Join(t.TempDir(), "wkbench")
buildWKBench := exec.Command("go", "build", "-o", wkbenchBin, "./cmd/wkbench")
buildWKBench.Dir = repoRoot(t)
buildWKBench.Env = append(os.Environ(), "GOWORK=off")
if out, err := buildWKBench.CombinedOutput(); err != nil {
	t.Fatalf("build wkbench binary for generated script: %v\n%s", err, out)
}
script := exec.Command("sh", "./scripts/check.sh")
script.Dir = dir
script.Env = append(os.Environ(), "GOWORK=off", "WKBENCH="+wkbenchBin)
if out, err := script.CombinedOutput(); err != nil {
	t.Fatalf("generated check script failed: %v\n%s", err, out)
}
```

- [ ] **Step 2: Run the failing generated-template test**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run TestPluginInitGeneratesBuildableExternalPlugin -count=1
```

Expected: FAIL because `scripts/check.sh` is not generated.

- [ ] **Step 3: Add file modes to plugin templates**

In `cmd/wkbench/plugin_init.go`, add `io/fs` to imports and replace the string
template map with:

```go
type pluginTemplateFile struct {
	Template string
	Mode     fs.FileMode
}
```

Inside `initPluginTemplate`, replace the `files := map[string]string{...}` with:

```go
files := map[string]pluginTemplateFile{
	"go.mod": {Template: pluginGoModTemplate, Mode: 0o644},
	"go.sum": {Template: pluginGoSumTemplate, Mode: 0o644},
	".gitignore": {Template: pluginGitignoreTemplate, Mode: 0o644},
	filepath.Join("cmd", spec.CommandName, "main.go"): {Template: pluginMainTemplate, Mode: 0o644},
	filepath.Join("units", "echo", "unit.go"): {Template: pluginUnitTemplate, Mode: 0o644},
	filepath.Join("units", "echo", "unit_test.go"): {Template: pluginUnitTestTemplate, Mode: 0o644},
	filepath.Join("examples", "echo.yaml"): {Template: pluginScenarioTemplate, Mode: 0o644},
	filepath.Join("scripts", "check.sh"): {Template: pluginCheckScriptTemplate, Mode: 0o755},
	"README.md": {Template: pluginReadmeTemplate, Mode: 0o644},
}
```

Update the write loop:

```go
for name, file := range files {
	path := filepath.Join(spec.Dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := renderPluginTemplate(name, file.Template, spec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, file.Mode); err != nil {
		return err
	}
}
```

- [ ] **Step 4: Add generated check script template**

Add this constant to `cmd/wkbench/plugin_init.go` before
`pluginReadmeTemplate`:

```go
const pluginCheckScriptTemplate = `#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
: "${WKBENCH:=wkbench}"

go test ./...
mkdir -p ./bin
go build -o ./bin/{{.CommandName}} ./cmd/{{.CommandName}}
"$WKBENCH" plugin check ./bin/{{.CommandName}} -scenario ./examples/echo.yaml
`
```

- [ ] **Step 5: Expand generated README template**

Replace `pluginReadmeTemplate` in `cmd/wkbench/plugin_init.go` with:

```go
const pluginReadmeTemplate = `# {{.PluginName}}

This is an external wkbench plugin generated by ` + "`wkbench plugin init`" + `.

## Local Check

Run the full author acceptance loop:

` + "```bash" + `
./scripts/check.sh
` + "```" + `

If ` + "`wkbench`" + ` is not on your PATH, point the script at a local binary:

` + "```bash" + `
WKBENCH=/path/to/wkbench ./scripts/check.sh
` + "```" + `

The script runs unit tests, builds the plugin binary, and checks the binary with
the generated scenario.

## Build

` + "```bash" + `
go test ./...
go build -o ./bin/{{.CommandName}} ./cmd/{{.CommandName}}
wkbench plugin check ./bin/{{.CommandName}} -scenario ./examples/echo.yaml
` + "```" + `

## Use With wkbench

` + "```bash" + `
wkbench plugin add {{.PluginName}} ./bin/{{.CommandName}}
wkbench plugin doctor
wkbench validate -scenario ./examples/echo.yaml
wkbench explain -scenario ./examples/echo.yaml
wkbench plan -scenario ./examples/echo.yaml
wkbench run -scenario ./examples/echo.yaml
` + "```" + `

Use the qualified unit kind in scenario YAML:

` + "```yaml" + `
units:
  echo:
    use: {{.QualifiedUse}}
    spec:
      message: hello from external plugin
` + "```" + `

## Phase 1 Port Limits

Ports crossing the plugin RPC boundary must be non-sensitive inline JSON data
ports. Large samples should be written as artifacts instead of inline outputs.

## Release Checklist

- ` + "`./scripts/check.sh`" + ` passes.
- The plugin binary and version are named for the release.
- Consumers know the qualified unit kind: ` + "`{{.QualifiedUse}}`" + `.
- Example scenarios validate before publishing.
`
```

- [ ] **Step 6: Run generated-template tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestPluginInitGeneratesBuildableExternalPlugin|TestPluginCheckScenarioValidatesExplainsAndPlans' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/wkbench/plugin_init.go cmd/wkbench/main_test.go
git commit -m "feat: generate plugin check scripts"
```

---

### Task 5: Authoring Documentation

**Files:**
- Modify: `docs/plugin-authoring.md`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Add a lightweight CLI usage test**

Add this test near the plugin command tests in `cmd/wkbench/main_test.go`:

```go
func TestPluginCommandUsageMentionsCheck(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"plugin"}, &stderr)
	if code != exitConfig {
		t.Fatalf("expected exitConfig, got %d stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "check") {
		t.Fatalf("plugin usage should mention check:\n%s", stderr.String())
	}
}
```

- [ ] **Step 2: Run the usage test**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run TestPluginCommandUsageMentionsCheck -count=1
```

Expected: PASS if Task 1 already updated usage.

- [ ] **Step 3: Update docs/plugin-authoring.md Host Usage**

In `docs/plugin-authoring.md`, update the generated-project flow to include
the check script and direct check command:

````markdown
Generate a standalone external plugin project:

```bash
GOWORK=off go run ./cmd/wkbench plugin init \
  -dir /tmp/acme-wkbench-plugin \
  -module example.com/acme/wkbench-plugin \
  -name acme.echo
cd /tmp/acme-wkbench-plugin
./scripts/check.sh
```

The generated check script runs `go test ./...`, builds the plugin binary, and
executes:

```bash
wkbench plugin check ./bin/acme-echo-plugin -scenario ./examples/echo.yaml
```
````

- [ ] **Step 4: Add command role descriptions**

In the "Project Plugin Config" section, replace the management command list
with:

```markdown
Management commands have distinct roles:

- `wkbench plugin check <name-or-path> [-scenario path]` is for plugin authors
  and CI. It starts one plugin, validates its manifest, and optionally runs
  scenario `validate`, `explain`, and `plan` with only that plugin loaded.
- `wkbench plugin inspect <name-or-path>` prints one plugin manifest.
- `wkbench plugin add <name> <path>` creates or updates `.wkbench/plugins.yaml`.
- `wkbench plugin list` prints configured plugins without starting them.
- `wkbench plugin doctor` starts enabled configured plugins, performs the
  handshake, and reports manifest/unit status.
- `wkbench plugin init -dir <dir> -module <module> -name <name>` generates a
  standalone Go plugin module.
```

- [ ] **Step 5: Run docs-adjacent tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestPluginCommandUsageMentionsCheck|TestPluginCheck' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add docs/plugin-authoring.md cmd/wkbench/main_test.go
git commit -m "docs: document plugin check workflow"
```

---

### Task 6: Final Verification

**Files:**
- No new files.

- [ ] **Step 1: Format Go files**

Run:

```bash
gofmt -w cmd/wkbench/plugin_check.go cmd/wkbench/plugin_commands.go cmd/wkbench/plugin_init.go cmd/wkbench/main.go cmd/wkbench/main_test.go
```

- [ ] **Step 2: Run focused command tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -count=1
```

Expected: PASS.

- [ ] **Step 3: Run pluginhost and SDK smoke tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost ./sdk/go/wkbench/plugin -count=1
```

Expected: PASS.

- [ ] **Step 4: Run relevant scenario validation**

Run:

```bash
tmpdir="$(mktemp -d)"
demo_plugin="$tmpdir/wkbench-demo-plugin"
GOWORK=off go build -o "$demo_plugin" ./plugins/demo/cmd/wkbench-demo-plugin
GOWORK=off go run ./cmd/wkbench plugin check "$demo_plugin" -scenario ./examples/plugin-echo.yaml
GOWORK=off go run ./cmd/wkbench -plugin "$demo_plugin" validate -scenario ./examples/plugin-echo.yaml
GOWORK=off go run ./cmd/wkbench -plugin "$demo_plugin" explain -scenario ./examples/plugin-echo.yaml
GOWORK=off go run ./cmd/wkbench -plugin "$demo_plugin" plan -scenario ./examples/plugin-echo.yaml
```

Expected: each command exits 0. `plugin check` prints `Plugin check: ok`.
`validate` prints `wkbench scenario is valid`.

- [ ] **Step 5: Run full test suite**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [ ] **Step 6: Check diff and status**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` exits 0. `git status --short` shows only
intentional changes if there is a final commit still pending.

- [ ] **Step 7: Final commit if needed**

If Task 6 produced any final cleanup edits:

```bash
git add cmd/wkbench docs/plugin-authoring.md
git commit -m "chore: finalize plugin check workflow"
```

If Task 6 produced no edits, do not create an empty commit.

---

## Self-Review

Spec coverage:

- `wkbench plugin check`: Tasks 1, 2, and 3.
- Manifest validation: Task 1.
- Configured-name and direct-path behavior: Task 2. Path-like targets bypass
  plugin config, while bare-name targets return plugin config read/parse errors
  before falling back to PATH.
- Timeout behavior: Task 2.
- Scenario `validate`, `explain`, and `plan`: Task 3.
- Scenario-mode plugin handshake timeout: Task 3.
- Generated `scripts/check.sh` and README: Task 4.
- `docs/plugin-authoring.md`: Task 5.
- Final verification: Task 6.

Scope check:

- Marketplace, install/update, binary cache, and version locks are excluded.
- Non-Go plugin scaffolds are excluded.
- Live WuKongIM runs are excluded.

Type consistency:

- Manifest validation uses existing `pluginhost.Plugin`, `pluginhost.Unit`,
  `contract.PortDef`, and `contract.PortMeta`.
- Scenario mode uses existing `defaultRegistry`, `loadExternalPlugins`,
  `loadScenario`, and `kernel.New`.
- Timeout support extends existing `inspectPluginManifest` without changing
  `plugin inspect` behavior.
- `pluginCommandSpec.HandshakeTimeout` is zero for normal command paths and set
  only by `plugin check -scenario`.
