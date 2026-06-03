// Package report writes wkbench run reports.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/WuKongIM/wkbench/benchkit/kernel"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
	wukongimport "github.com/WuKongIM/wkbench/benchkit/ports/wukongim"
)

// WriteDir writes a compact JSON and Markdown report directory.
func WriteDir(dir string, result kernel.Result) error {
	if dir == "" {
		return fmt.Errorf("report directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(summaryMarkdown(result)), 0o644)
}

func summaryMarkdown(result kernel.Result) string {
	unitNames := make([]string, 0, len(result.Units))
	for name := range result.Units {
		unitNames = append(unitNames, name)
	}
	sort.Strings(unitNames)
	out := fmt.Sprintf("# wkbench run %s\n\nstatus: `%s`\n\n", result.RunID, result.Status)
	if len(unitNames) == 0 {
		return out
	}
	out += "## Units\n\n"
	for _, name := range unitNames {
		unit := result.Units[name]
		out += fmt.Sprintf("- `%s` `%s` `%s`\n", name, unit.Kind, unit.Status)
		if unit.ElapsedMS > 0 {
			out += fmt.Sprintf("  - timing: elapsed `%dms`, started `%s`, ended `%s`\n", unit.ElapsedMS, unit.StartedAt, unit.EndedAt)
		}
		outputNames := make([]string, 0, len(unit.Outputs))
		for outputName := range unit.Outputs {
			outputNames = append(outputNames, outputName)
		}
		sort.Strings(outputNames)
		for _, outputName := range outputNames {
			out += formatOutput(outputName, unit.Outputs[outputName])
		}
		metricNames := make([]string, 0, len(unit.Metrics))
		for metricName := range unit.Metrics {
			metricNames = append(metricNames, metricName)
		}
		sort.Strings(metricNames)
		for _, metricName := range metricNames {
			out += formatMetric(metricName, unit.Metrics[metricName])
		}
		artifactNames := make([]string, 0, len(unit.Artifacts))
		for artifactName := range unit.Artifacts {
			artifactNames = append(artifactNames, artifactName)
		}
		sort.Strings(artifactNames)
		for _, artifactName := range artifactNames {
			out += formatArtifact(artifactName, unit.Artifacts[artifactName])
		}
		for _, cleanup := range unit.Cleanup {
			out += formatCleanup(cleanup)
		}
	}
	return out
}

func formatOutput(name string, output kernel.OutputResult) string {
	prefix := fmt.Sprintf("  - output `%s` `%s`", name, output.Type)
	if output.Value == nil {
		return prefix + "\n"
	}
	if output.Type == wukongimport.MetricsSummaryV1 {
		if formatted, ok := formatWuKongIMMetricsSummary(output.Value); ok {
			return prefix + ": " + formatted + "\n"
		}
	}
	return prefix + ": " + formatOutputValue(output.Value) + "\n"
}

func formatOutputValue(value any) string {
	switch v := value.(type) {
	case trafficport.Summary:
		return fmt.Sprintf("sendack_ok: `%d`, sendack_errors: `%d`, sendack_error_rate: `%.4f`, elapsed_ms: `%d`, actual_qps: `%.2f`, last_message_id: `%d`", v.SendackOK, v.SendackErrors, v.SendackErrorRate(), v.ElapsedMS, v.ActualQPS(), v.LastMessageID)
	case wukongimport.MetricsSummary:
		formatted, _ := formatWuKongIMMetricsSummary(v)
		return formatted
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprintf("value: `%v`", value)
		}
		return fmt.Sprintf("value: `%s`", data)
	}
}

func formatWuKongIMMetricsSummary(value any) (string, bool) {
	var scrapes, samples, errors int64
	var p95, p99 float64

	switch v := value.(type) {
	case wukongimport.MetricsSummary:
		scrapes = v.ScrapeTicks
		samples = v.SelectedSamples
		p95 = v.LatencyP95MS
		p99 = v.LatencyP99MS
		for _, node := range v.Nodes {
			errors += node.Errors
		}
	case map[string]any:
		scrapes = int64Value(v["scrape_ticks"])
		samples = int64Value(v["selected_samples"])
		p95 = float64Value(v["latency_p95_ms"])
		p99 = float64Value(v["latency_p99_ms"])
		errors = wukongIMMetricsSummaryErrors(v["nodes"])
	default:
		return "", false
	}

	return fmt.Sprintf("scrapes: `%d`, errors: `%d`, samples: `%d`, latency_p95: `%.2fms`, latency_p99: `%.2fms`", scrapes, errors, samples, p95, p99), true
}

func wukongIMMetricsSummaryErrors(nodes any) int64 {
	var errors int64
	switch v := nodes.(type) {
	case []wukongimport.NodeScrapeSummary:
		for _, node := range v {
			errors += node.Errors
		}
	case []any:
		for _, node := range v {
			if fields, ok := node.(map[string]any); ok {
				errors += int64Value(fields["errors"])
			}
		}
	}
	return errors
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func float64Value(value any) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	default:
		return 0
	}
}

func formatMetric(name string, metric kernel.MetricResult) string {
	switch metric.Type {
	case "duration":
		avg := 0.0
		if metric.Count > 0 {
			avg = metric.Sum / float64(metric.Count)
		}
		line := fmt.Sprintf(
			"  - metric `%s` `duration`: count `%d`, avg `%s`",
			name,
			metric.Count,
			formatMilliseconds(avg),
		)
		if metric.P95 != 0 || metric.P99 != 0 {
			line += fmt.Sprintf(", p95 `%s`, p99 `%s`", formatMilliseconds(metric.P95), formatMilliseconds(metric.P99))
		}
		line += fmt.Sprintf(", min `%s`, max `%s`\n", formatMilliseconds(metric.Min), formatMilliseconds(metric.Max))
		return line
	default:
		metricType := metric.Type
		if metricType == "" {
			metricType = "counter"
		}
		return fmt.Sprintf(
			"  - metric `%s` `%s`: count `%d`, sum `%s`\n",
			name,
			metricType,
			metric.Count,
			formatNumber(metric.Sum),
		)
	}
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatMilliseconds(value float64) string {
	return fmt.Sprintf("%.2fms", value*1000)
}

func formatArtifact(name string, artifact kernel.ArtifactResult) string {
	return fmt.Sprintf("  - artifact `%s`: `%s`, %s\n", name, artifact.Path, formatBytes(artifact.SizeBytes))
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}

func formatCleanup(cleanup kernel.CleanupResult) string {
	if cleanup.Error == "" {
		return fmt.Sprintf("  - cleanup `%s`: ok\n", cleanup.Output)
	}
	return fmt.Sprintf("  - cleanup `%s`: %s\n", cleanup.Output, cleanup.Error)
}
