package report_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/kernel"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
	"github.com/WuKongIM/wkbench/benchkit/report"
)

func TestWriteDirCreatesJSONAndMarkdownSummary(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "demo",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"traffic": {Kind: "traffic.group_send/v1", Status: kernel.StatusCompleted},
		},
	}
	if err := report.WriteDir(dir, result); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err != nil {
		t.Fatalf("report.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "summary.md")); err != nil {
		t.Fatalf("summary.md missing: %v", err)
	}
}

func TestWriteDirIncludesTrafficSummary(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "demo",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"traffic": {
				Kind:   "traffic.group_send/v1",
				Status: kernel.StatusCompleted,
				Outputs: map[string]kernel.OutputResult{
					"summary": {
						Type:  trafficport.SummaryV1,
						Value: trafficport.Summary{SendackOK: 9, SendackErrors: 1, ElapsedMS: 2000, LastMessageID: 42},
					},
				},
			},
		},
	}
	if err := report.WriteDir(dir, result); err != nil {
		t.Fatalf("write report: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"sendack_ok: `9`", "sendack_errors: `1`", "sendack_error_rate: `0.1000`", "elapsed_ms: `2000`", "actual_qps: `4.50`"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary.md missing %q:\n%s", want, text)
		}
	}
}

func TestWriteDirIncludesMetrics(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "demo",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"traffic": {
				Kind:   "traffic.group_send/v1",
				Status: kernel.StatusCompleted,
				Metrics: map[string]kernel.MetricResult{
					"send_attempt_total": {
						Type:  "counter",
						Count: 2,
						Sum:   3,
					},
					"sendack_latency": {
						Type:  "duration",
						Count: 2,
						Sum:   0.003,
						Min:   0.001,
						Max:   0.002,
						P95:   0.002,
						P99:   0.002,
					},
				},
			},
		},
	}
	if err := report.WriteDir(dir, result); err != nil {
		t.Fatalf("write report: %v", err)
	}
	jsonData, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	jsonText := string(jsonData)
	for _, want := range []string{`"metrics"`, `"send_attempt_total"`, `"sendack_latency"`, `"p95"`, `"p99"`} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("report.json missing %q:\n%s", want, jsonText)
		}
	}
	markdownData, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	markdown := string(markdownData)
	for _, want := range []string{
		"metric `send_attempt_total` `counter`: count `2`, sum `3`",
		"metric `sendack_latency` `duration`: count `2`, avg `1.50ms`, p95 `2.00ms`, p99 `2.00ms`, min `1.00ms`, max `2.00ms`",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("summary.md missing %q:\n%s", want, markdown)
		}
	}
}

func TestWriteDirIncludesCleanupErrors(t *testing.T) {
	dir := t.TempDir()
	result := kernel.Result{
		RunID:  "demo",
		Status: kernel.StatusCompleted,
		Units: map[string]kernel.UnitResult{
			"sessions": {
				Kind:   "wkproto.session_pool/v1",
				Status: kernel.StatusCompleted,
				Cleanup: []kernel.CleanupResult{
					{Output: "group_sender", Error: "close failed"},
				},
			},
		},
	}
	if err := report.WriteDir(dir, result); err != nil {
		t.Fatalf("write report: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "cleanup `group_sender`: close failed") {
		t.Fatalf("summary.md missing cleanup error:\n%s", text)
	}
}
