// Package report writes wkbench run reports.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/WuKongIM/wkbench/benchkit/kernel"
	trafficport "github.com/WuKongIM/wkbench/benchkit/ports/traffic"
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
		outputNames := make([]string, 0, len(unit.Outputs))
		for outputName := range unit.Outputs {
			outputNames = append(outputNames, outputName)
		}
		sort.Strings(outputNames)
		for _, outputName := range outputNames {
			out += formatOutput(outputName, unit.Outputs[outputName])
		}
	}
	return out
}

func formatOutput(name string, output kernel.OutputResult) string {
	prefix := fmt.Sprintf("  - output `%s` `%s`", name, output.Type)
	if output.Value == nil {
		return prefix + "\n"
	}
	return prefix + ": " + formatOutputValue(output.Value) + "\n"
}

func formatOutputValue(value any) string {
	switch v := value.(type) {
	case trafficport.Summary:
		return fmt.Sprintf("sendack_ok: `%d`, sendack_errors: `%d`, sendack_error_rate: `%.4f`, last_message_id: `%d`", v.SendackOK, v.SendackErrors, v.SendackErrorRate(), v.LastMessageID)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprintf("value: `%v`", value)
		}
		return fmt.Sprintf("value: `%s`", data)
	}
}
