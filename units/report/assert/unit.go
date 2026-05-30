// Package assert implements report.assert/v1.
package assert

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const kind = "report.assert/v1"

// Unit evaluates simple rules over a traffic summary.
type Unit struct{}

// Spec configures report assertions.
type Spec struct {
	// Rules are evaluated in order.
	Rules []Rule `json:"rules" yaml:"rules"`
}

// Rule describes one assertion.
type Rule struct {
	// Metric selects the input metric, for example sendack_error_rate.
	Metric string `json:"metric" yaml:"metric"`
	// Op is the comparison operator, currently eq.
	Op string `json:"op" yaml:"op"`
	// Value is the numeric comparison value.
	Value float64 `json:"value" yaml:"value"`
}

// Result records assertion status.
type Result struct {
	// Passed is true when every rule passed.
	Passed bool `json:"passed"`
	// Failures lists failed rule descriptions.
	Failures []string `json:"failures,omitempty"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Report assertions",
		Description: "Evaluates simple assertions over traffic summaries.",
		Inputs: []contract.PortDef{
			{Name: "summary", Type: trafficport.SummaryV1},
		},
		Outputs: []contract.PortDef{
			{Name: "result", Type: contract.PortType("port.report.assertion_result/v1")},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	for idx, rule := range spec.Rules {
		if strings.TrimSpace(rule.Metric) == "" {
			return fmt.Errorf("rules[%d].metric is required", idx)
		}
		if strings.TrimSpace(rule.Op) == "" {
			return fmt.Errorf("rules[%d].op is required", idx)
		}
	}
	return nil
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	summary, err := contract.Input[trafficport.Summary](env, "summary")
	if err != nil {
		return err
	}
	result := Result{Passed: true}
	for _, rule := range spec.Rules {
		value, err := metricValue(summary, rule.Metric)
		if err != nil {
			return err
		}
		if !compare(value, rule.Op, rule.Value) {
			result.Passed = false
			result.Failures = append(result.Failures, fmt.Sprintf("%s %s %g failed: got %g", rule.Metric, rule.Op, rule.Value, value))
		}
	}
	if err := env.SetOutput("result", result); err != nil {
		return err
	}
	if !result.Passed {
		return fmt.Errorf("assertions failed: %s", strings.Join(result.Failures, "; "))
	}
	return nil
}

func decodeSpec(env contract.ValidateEnv) (Spec, error) {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

func metricValue(summary trafficport.Summary, metric string) (float64, error) {
	switch strings.TrimSpace(metric) {
	case "sendack_error_rate":
		return summary.SendackErrorRate(), nil
	case "sendack_ok":
		return float64(summary.SendackOK), nil
	case "sendack_errors":
		return float64(summary.SendackErrors), nil
	default:
		return 0, fmt.Errorf("unsupported assertion metric %q", metric)
	}
}

func compare(got float64, op string, want float64) bool {
	switch strings.TrimSpace(op) {
	case "eq":
		return got == want
	case "lt":
		return got < want
	case "lte":
		return got <= want
	case "gt":
		return got > want
	case "gte":
		return got >= want
	default:
		return false
	}
}
