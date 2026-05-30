// Package dsl parses wkbench v2 scenario YAML.
package dsl

import (
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Scenario is the top-level wkbench/v2 graph document.
type Scenario struct {
	// Version is the scenario schema version.
	Version string `json:"version" yaml:"version"`
	// Run contains scenario runtime metadata.
	Run RunConfig `json:"run" yaml:"run"`
	// Vars contains simple substitution values used in specs.
	Vars map[string]any `json:"vars" yaml:"vars"`
	// Units contains scenario-local unit nodes keyed by name.
	Units map[string]UnitNode `json:"units" yaml:"units"`
}

// RunConfig contains shared scenario runtime settings.
type RunConfig struct {
	// ID identifies this run in reports.
	ID string `json:"id" yaml:"id"`
	// Duration is the measured run duration.
	Duration time.Duration `json:"duration" yaml:"duration"`
	// Seed makes planning deterministic when units use randomization.
	Seed int64 `json:"seed" yaml:"seed"`
	// ReportDir is the optional report output directory.
	ReportDir string `json:"report_dir" yaml:"report_dir"`
}

// UnitNode describes one scenario unit instance.
type UnitNode struct {
	// Use selects the registered unit kind.
	Use string `json:"use" yaml:"use"`
	// Inputs maps unit input names to resource references such as traffic.summary.
	Inputs map[string]string `json:"inputs" yaml:"inputs"`
	// After adds ordering dependencies that do not carry data.
	After []string `json:"after" yaml:"after"`
	// Spec is decoded only by the selected unit.
	Spec map[string]any `json:"spec" yaml:"spec"`
}

// Parse reads a scenario and expands whole-value ${var} references.
func Parse(r io.Reader) (Scenario, error) {
	var scenario Scenario
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&scenario); err != nil {
		return Scenario{}, err
	}
	if scenario.Vars == nil {
		scenario.Vars = make(map[string]any)
	}
	for name, node := range scenario.Units {
		expanded, err := expandValue(node.Spec, scenario.Vars)
		if err != nil {
			return Scenario{}, fmt.Errorf("unit %q: %w", name, err)
		}
		if expanded == nil {
			node.Spec = nil
		} else {
			spec, ok := expanded.(map[string]any)
			if !ok {
				return Scenario{}, fmt.Errorf("unit %q spec must be an object", name)
			}
			node.Spec = spec
		}
		scenario.Units[name] = node
	}
	return scenario, nil
}

func expandValue(value any, vars map[string]any) (any, error) {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "${") && strings.HasSuffix(typed, "}") && len(typed) > 3 {
			key := strings.TrimSuffix(strings.TrimPrefix(typed, "${"), "}")
			resolved, ok := vars[key]
			if !ok {
				return nil, fmt.Errorf("unknown variable %q", key)
			}
			return resolved, nil
		}
		return typed, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			expanded, err := expandValue(v, vars)
			if err != nil {
				return nil, err
			}
			out[k] = expanded
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(typed))
		for _, v := range typed {
			expanded, err := expandValue(v, vars)
			if err != nil {
				return nil, err
			}
			out = append(out, expanded)
		}
		return out, nil
	default:
		return value, nil
	}
}
