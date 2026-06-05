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

func runPluginCheck(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: wkbench plugin check <path>")
		return exitConfig
	}
	options := pluginCheckOptions{
		Target:  args[0],
		Timeout: pluginManifestTimeout,
	}
	target := pluginCheckTarget{
		Label: options.Target,
		Path:  pluginCheckCleanPath(options.Target),
	}
	manifest, err := inspectPluginManifest(target.Path)
	if err != nil {
		manifest = pluginhost.Plugin{Source: target.Path}
		writePluginCheckReport(stderr, manifest, []pluginCheckIssue{{
			Subject: target.Label,
			Message: fmt.Sprintf("inspect failed: %v", err),
		}})
		return exitConfig
	}
	issues := validatePluginCheckManifest(manifest)
	writePluginCheckReport(stderr, manifest, issues)
	if len(issues) > 0 {
		return exitConfig
	}
	return exitOK
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

func pluginCheckCleanPath(path string) string {
	return filepath.Clean(path)
}
