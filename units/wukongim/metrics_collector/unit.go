// Package metrics_collector implements wukongim.metrics_collector/v1.
package metrics_collector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

// Kind is the stable unit kind for the WuKongIM metrics collector.
const Kind = "wukongim.metrics_collector/v1"

const (
	defaultInterval          = time.Second
	defaultTimeout           = 800 * time.Millisecond
	defaultPath              = "/metrics"
	defaultMaxSummaryMetrics = 100
)

// Unit collects WuKongIM Prometheus metrics while foreground units run.
type Unit struct{}

type collectorSpec struct {
	Interval             contract.Duration `json:"interval" yaml:"interval"`
	Timeout              contract.Duration `json:"timeout" yaml:"timeout"`
	Path                 string            `json:"path" yaml:"path"`
	Include              []string          `json:"include" yaml:"include"`
	Exclude              []string          `json:"exclude" yaml:"exclude"`
	FailOnScrapeError    bool              `json:"fail_on_scrape_error" yaml:"fail_on_scrape_error"`
	MaxConsecutiveErrors int               `json:"max_consecutive_errors" yaml:"max_consecutive_errors"`
	MaxSummaryMetrics    int               `json:"max_summary_metrics" yaml:"max_summary_metrics"`
}

type planShard struct {
	Interval string `json:"interval"`
	Path     string `json:"path"`
}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        Kind,
		Title:       "WuKongIM metrics collector",
		Description: "Collects WuKongIM Prometheus metrics and publishes a compact summary.",
		Inputs: []contract.PortDef{
			{Name: "target", Type: targetport.TargetV1},
		},
		Outputs: []contract.PortDef{
			{Name: "summary", Type: wukongimport.MetricsSummaryV1},
		},
		Metrics: []contract.MetricDef{
			{Name: "scrape_success_total", Type: "counter"},
			{Name: "scrape_error_total", Type: "counter"},
			{Name: "scrape_parse_error_total", Type: "counter"},
			{Name: "scrape_latency", Type: "duration"},
		},
		Artifacts: []contract.ArtifactDef{
			{Name: "metrics.jsonl", ContentType: "application/jsonl"},
		},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := decodeSpec(env)
	if err != nil {
		return err
	}
	if spec.Interval.Duration <= 0 {
		return fmt.Errorf("interval must be greater than zero")
	}
	if spec.Timeout.Duration <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if spec.Path == "" {
		return fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(spec.Path, "/") {
		return fmt.Errorf("path must start with /")
	}
	if _, err := newMetricFilter(spec); err != nil {
		return err
	}
	if spec.MaxConsecutiveErrors < 0 {
		return fmt.Errorf("max_consecutive_errors must not be negative")
	}
	if spec.MaxSummaryMetrics < 0 {
		return fmt.Errorf("max_summary_metrics must not be negative")
	}
	return nil
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	spec, err := decodeSpec(env)
	if err != nil {
		return contract.Plan{}, err
	}
	return contract.Plan{
		UnitName: env.UnitName(),
		Shards: []any{
			planShard{Interval: spec.Interval.Duration.String(), Path: spec.Path},
		},
	}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	return fmt.Errorf("%s is a background unit; use Start", Kind)
}

func decodeSpec(env contract.ValidateEnv) (collectorSpec, error) {
	spec := collectorSpec{
		Interval: contract.Duration{Duration: defaultInterval},
		Timeout:  contract.Duration{Duration: defaultTimeout},
		Path:     defaultPath,
	}
	if err := env.DecodeSpec(&spec); err != nil {
		return collectorSpec{}, err
	}
	spec.Path = strings.TrimSpace(spec.Path)
	spec.Include = trimStrings(spec.Include)
	spec.Exclude = trimStrings(spec.Exclude)
	if spec.MaxSummaryMetrics == 0 {
		spec.MaxSummaryMetrics = defaultMaxSummaryMetrics
	}
	return spec, nil
}

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
