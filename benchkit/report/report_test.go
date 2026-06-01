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
						Value: trafficport.Summary{SendackOK: 9, SendackErrors: 1, LastMessageID: 42},
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
	for _, want := range []string{"sendack_ok: `9`", "sendack_errors: `1`", "sendack_error_rate: `0.1000`"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary.md missing %q:\n%s", want, text)
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
