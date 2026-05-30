package report_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/kernel"
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
