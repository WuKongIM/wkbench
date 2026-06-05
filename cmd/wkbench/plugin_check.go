package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/kernel"
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

func runPluginCheck(args []string, stderr io.Writer) int {
	options, code := parsePluginCheckArgs(args, stderr)
	if code != exitOK {
		return code
	}
	target, err := resolvePluginCheckTarget(options.Target)
	if err != nil {
		fmt.Fprintf(stderr, "plugin check failed: %v\n", err)
		return exitConfig
	}
	if options.ScenarioPath != "" {
		return runPluginCheckScenario(target, options.ScenarioPath, options.Timeout, stderr)
	}
	return runPluginCheckManifest(target, options.Timeout, stderr)
}

func runPluginCheckManifest(target pluginCheckTarget, timeout time.Duration, stderr io.Writer) int {
	manifest, err := inspectPluginManifestWithTimeout(target.Path, timeout)
	if err != nil {
		writePluginCheckInspectError(stderr, target, err)
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

func writePluginCheckInspectError(w io.Writer, target pluginCheckTarget, err error) {
	if strings.HasPrefix(err.Error(), "start plugin:") {
		fmt.Fprintf(w, "plugin %s failed to start: %v\n", target.Label, err)
		return
	}
	fmt.Fprintf(w, "plugin check failed: %v\n", err)
}

func runPluginCheckScenario(target pluginCheckTarget, scenarioPath string, timeout time.Duration, stderr io.Writer) int {
	fmt.Fprintf(stderr, "Scenario: %s\n", scenarioPath)
	if code := runPluginCheckManifest(target, timeout, stderr); code != exitOK {
		return code
	}
	reg := defaultRegistry()
	clients, code := loadExternalPlugins(reg, []pluginCommandSpec{{
		Label:            target.Label,
		Path:             target.Path,
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

func validatePluginCheckManifest(manifest pluginhost.Plugin) []pluginCheckIssue {
	var issues []pluginCheckIssue
	if strings.TrimSpace(manifest.Name) == "" {
		issues = append(issues, pluginCheckIssue{Subject: "plugin", Message: "plugin name is required"})
	}
	if manifest.Protocol != pluginProtocolV1 {
		issues = append(issues, pluginCheckIssue{
			Subject: "plugin",
			Message: fmt.Sprintf("plugin protocol must be %s", pluginProtocolV1),
		})
	}
	seenKinds := map[string]bool{}
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
			if seenKinds[unit.Kind] {
				issues = append(issues, pluginCheckIssue{
					Subject: subject,
					Message: fmt.Sprintf("unit kind %q is declared more than once", unit.Kind),
				})
			}
			seenKinds[unit.Kind] = true
		}
		issues = append(issues, validatePluginCheckPorts(unit.Kind, "input", unit.Inputs)...)
		issues = append(issues, validatePluginCheckPorts(unit.Kind, "output", unit.Outputs)...)
		for _, artifact := range unit.Artifacts {
			if strings.TrimSpace(artifact.Name) == "" {
				issues = append(issues, pluginCheckIssue{Subject: subject, Message: "artifact name is required"})
			}
		}
	}
	return issues
}

func validatePluginCheckPorts(unitKind, direction string, ports []contract.PortDef) []pluginCheckIssue {
	subject := unitKind
	if subject == "" {
		subject = "unit"
	}
	var issues []pluginCheckIssue
	for _, port := range ports {
		label := direction
		if strings.TrimSpace(port.Name) != "" {
			label = direction + " " + port.Name
		}
		if strings.TrimSpace(port.Name) == "" {
			issues = append(issues, pluginCheckIssue{Subject: subject, Message: direction + " port name is required"})
		}
		if strings.TrimSpace(string(port.Type)) == "" {
			issues = append(issues, pluginCheckIssue{Subject: subject, Message: label + " type is required"})
		}
		meta := port.Metadata()
		if meta.Boundary != contract.PortBoundaryData {
			issues = append(issues, pluginCheckIssue{
				Subject: subject,
				Message: fmt.Sprintf("%s boundary must be %s", label, contract.PortBoundaryData),
			})
		}
		if meta.Transport != contract.PortTransportInline {
			issues = append(issues, pluginCheckIssue{
				Subject: subject,
				Message: fmt.Sprintf("%s transport must be %s", label, contract.PortTransportInline),
			})
		}
		if meta.Sensitive {
			issues = append(issues, pluginCheckIssue{
				Subject: subject,
				Message: fmt.Sprintf("%s is sensitive; sensitive inline data ports cannot cross plugin RPC in Phase 1", label),
			})
		}
		if port.Meta.MaxPayloadBytes < 0 {
			issues = append(issues, pluginCheckIssue{
				Subject: subject,
				Message: label + " max_payload_bytes must not be negative",
			})
		}
		if len(meta.Encodings) > 0 && !encodingsAllowJSON(meta.Encodings) {
			issues = append(issues, pluginCheckIssue{
				Subject: subject,
				Message: label + " encodings must include json for Phase 1 inline transport",
			})
		}
	}
	return issues
}

func hasVersionSuffixForCheck(kind string) bool {
	idx := strings.LastIndex(kind, "/v")
	if idx <= 0 || idx+2 >= len(kind) {
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
		if strings.TrimSpace(encoding) == "json" {
			return true
		}
	}
	return false
}

func writePluginCheckReport(w io.Writer, manifest pluginhost.Plugin, issues []pluginCheckIssue) {
	if len(issues) > 0 {
		fmt.Fprintln(w, "Plugin check: failed")
	} else {
		fmt.Fprintln(w, "Plugin check: ok")
	}
	fmt.Fprintf(w, "Plugin: %s\n", manifest.Name)
	fmt.Fprintf(w, "Version: %s\n", manifest.Version)
	fmt.Fprintf(w, "Protocol: %s\n", manifest.Protocol)
	if manifest.Source != "" {
		fmt.Fprintf(w, "Source: %s\n", manifest.Source)
	}
	if len(manifest.Units) > 0 {
		fmt.Fprintln(w, "Units:")
		for _, unit := range manifest.Units {
			subject := unit.Kind
			if subject == "" {
				subject = "unit"
			}
			status := "ok"
			if len(issuesForSubject(issues, subject)) > 0 {
				status = "failed"
			}
			fmt.Fprintf(w, "  - %s: %s", unit.Kind, status)
			if unit.Background {
				fmt.Fprint(w, " background=true")
			}
			fmt.Fprintln(w)
			writePluginCheckPorts(w, "inputs", unit.Inputs)
			writePluginCheckPorts(w, "outputs", unit.Outputs)
			if len(unit.Artifacts) > 0 {
				fmt.Fprintln(w, "    artifacts:")
				for _, artifact := range unit.Artifacts {
					fmt.Fprintf(w, "      - %s", artifact.Name)
					if artifact.ContentType != "" {
						fmt.Fprintf(w, " content_type=%s", artifact.ContentType)
					}
					fmt.Fprintln(w)
				}
			}
		}
	}
	if len(issues) > 0 {
		fmt.Fprintln(w, "Issues:")
		for _, issue := range issues {
			if issue.Subject != "" {
				fmt.Fprintf(w, "  - %s: %s\n", issue.Subject, issue.Message)
			} else {
				fmt.Fprintf(w, "  - %s\n", issue.Message)
			}
		}
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
	return fmt.Sprintf("boundary=%s transport=%s reportable=%t sensitive=%t", meta.Boundary, meta.Transport, meta.Reportable, meta.Sensitive)
}
