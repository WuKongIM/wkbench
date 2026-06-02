package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSmokeScriptIsSyntaxCheckedAndRunsValidateBeforeRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash smoke script is for unix-like developer environments")
	}
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	script := filepath.Join(root, "scripts", "smoke-wukongim-single-node.sh")
	cmd := exec.Command("bash", "-n", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	validate := strings.Index(text, "go run ./cmd/wkbench validate -scenario")
	run := strings.Index(text, "go run ./cmd/wkbench run -scenario")
	if validate < 0 || run < 0 || validate > run {
		t.Fatalf("script must validate before run, got:\n%s", text)
	}
	if !strings.Contains(text, "WKBENCH_SCENARIO") {
		t.Fatalf("script should allow WKBENCH_SCENARIO override")
	}
}

func TestMixedSendRateSmokeScriptUsesMixedScenario(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash smoke script is for unix-like developer environments")
	}
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	script := filepath.Join(root, "scripts", "smoke-wukongim-send-rate-mixed.sh")
	cmd := exec.Command("bash", "-n", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "examples/wukongim-send-rate-mixed.yaml") {
		t.Fatalf("script should default to mixed send-rate scenario, got:\n%s", text)
	}
	validate := strings.Index(text, "go run ./cmd/wkbench validate -scenario")
	run := strings.Index(text, "go run ./cmd/wkbench run -scenario")
	if validate < 0 || run < 0 || validate > run {
		t.Fatalf("script must validate before run, got:\n%s", text)
	}
	if !strings.Contains(text, "WKBENCH_SCENARIO") {
		t.Fatalf("script should allow WKBENCH_SCENARIO override")
	}
}

func TestThreeNodeStartupScriptUsesSiblingWuKongIMRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash startup script is for unix-like developer environments")
	}
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	script := filepath.Join(root, "scripts", "start-wukongimv2-three-nodes.sh")
	cmd := exec.Command("bash", "-n", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}

	cmd = exec.Command("bash", script, "--dry-run", "--no-build")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	text := string(out)
	wukongRoot := expectedWuKongIMRoot(t, root)
	for _, want := range []string{
		"repo_root=" + wukongRoot,
		"runtime_root=" + root,
		"node1_config=" + filepath.Join(wukongRoot, "scripts", "wukongimv2", "wukongimv2-node1.conf"),
		"node1_ready=http://127.0.0.1:5011/readyz",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}

	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `GOWORK="${GOWORK:-off}" go build`) {
		t.Fatalf("script should build WuKongIM with GOWORK defaulting to off")
	}
}

func expectedWuKongIMRoot(t *testing.T, root string) string {
	t.Helper()
	for _, candidate := range []string{
		filepath.Join(root, "..", "WuKongIM"),
		filepath.Join(root, "..", "..", "..", "WuKongIM"),
		filepath.Join(root, ".."),
	} {
		candidate = filepath.Clean(candidate)
		if _, err := os.Stat(filepath.Join(candidate, "cmd", "wukongimv2")); err == nil {
			return candidate
		}
	}
	t.Fatalf("cannot find expected WuKongIM root near %s", root)
	return ""
}

func TestSendRateSweepScriptSyntaxAndRequiredTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	script := sweepScriptPath(t)
	cmd := exec.Command("/bin/bash", "-n", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"command -v jq",
		"GOWORK=off go run ./cmd/wkbench validate -scenario",
		"GOWORK=off go run ./cmd/wkbench run -scenario",
		"console.txt",
		"summary.csv",
		"summary.md",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sweep script missing %q", want)
		}
	}
	validate := strings.Index(text, "go run ./cmd/wkbench validate -scenario")
	run := strings.Index(text, "go run ./cmd/wkbench run -scenario")
	if validate < 0 || run < 0 || validate > run {
		t.Fatalf("sweep script must validate before run")
	}
}

func TestSendRateSweepScriptRequiresJQForRealRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	script := sweepScriptPath(t)
	cmd := exec.Command("/bin/bash", script,
		"--mode", "person",
		"--rates", "1",
		"--duration", "1ms",
		"--out-dir", t.TempDir(),
		"--no-start-target",
	)
	cmd.Dir = filepath.Dir(filepath.Dir(scriptPath(t)))
	pathDir := t.TempDir()
	for _, tool := range []string{"dirname", "date"} {
		resolved, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("find %s: %v", tool, err)
		}
		if err := os.Symlink(resolved, filepath.Join(pathDir, tool)); err != nil {
			t.Fatalf("symlink %s: %v", tool, err)
		}
	}
	cmd.Env = append(os.Environ(), "PATH="+pathDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing jq failure, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "jq is required") {
		t.Fatalf("missing jq error should be clear, got:\n%s", out)
	}
}

func TestSendRateSweepDryRunRendersModeScenarios(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	cases := []struct {
		name    string
		mode    string
		want    []string
		mustNot []string
	}{
		{
			name: "person",
			mode: "person",
			want: []string{
				"use: identity.person_pairs",
				"use: traffic.send",
				"rate: 100/s",
				"max_in_flight: 40",
				"person_traffic:",
			},
			mustNot: []string{"group_traffic:", "use: wukongim.prepare_group_channels"},
		},
		{
			name: "group",
			mode: "group",
			want: []string{
				"use: wukongim.prepare_group_channels",
				"use: traffic.send",
				"rate: 100/s",
				"max_in_flight: 40",
				"group_traffic:",
			},
			mustNot: []string{"person_traffic:", "use: identity.person_pairs"},
		},
		{
			name: "mixed",
			mode: "mixed",
			want: []string{
				"person_traffic:",
				"group_traffic:",
				"rate: 80/s",
				"rate: 20/s",
				"max_in_flight: 32",
				"max_in_flight: 8",
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			outDir, _ := runSweepDryRun(t, "--mode", tt.mode)
			data, err := os.ReadFile(filepath.Join(outDir, "steps", "0001-100qps", "scenario.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("%s scenario missing %q:\n%s", tt.mode, want, text)
				}
			}
			for _, bad := range tt.mustNot {
				if strings.Contains(text, bad) {
					t.Fatalf("%s scenario should not contain %q:\n%s", tt.mode, bad, text)
				}
			}
		})
	}
}

func TestSendRateSweepDryRunCapsMaxInFlight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	outDir, _ := runSweepDryRun(t,
		"--mode", "person",
		"--rates", "100000",
		"--max-in-flight-cap", "1234",
	)
	data, err := os.ReadFile(filepath.Join(outDir, "steps", "0001-100000qps", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "max_in_flight: 1234") {
		t.Fatalf("scenario should cap max_in_flight at 1234:\n%s", data)
	}
}

func TestSendRateSweepDryRunHonorsGroupCount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	outDir, _ := runSweepDryRun(t,
		"--mode", "group",
		"--groups", "7",
	)
	data, err := os.ReadFile(filepath.Join(outDir, "steps", "0001-100qps", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "count: 7") {
		t.Fatalf("scenario should honor --groups 7:\n%s", data)
	}
}

func TestSendRateSweepScriptExtractsReportJSONFields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash sweep script is for unix-like developer environments")
	}
	data, err := os.ReadFile(sweepScriptPath(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`.units[$unit].outputs.summary.value.sendack_ok`,
		`.units[$unit].outputs.summary.value.sendack_errors`,
		`.units[$unit].metrics.sendack_latency.sum`,
		`.units[$unit].metrics.sendack_latency.min`,
		`.units[$unit].metrics.sendack_latency.max`,
		`avg_ms`,
		`summary.csv`,
		`highest_passing_qps`,
		`first_failing_qps`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sweep script missing result extraction fragment %q", want)
		}
	}
}

func TestReadmeDocumentsSendRateSweep(t *testing.T) {
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"bench-wukongim-three-node-send-rate-sweep.sh",
		"--mode mixed",
		"--rates 100,200,500",
		"--duration 2m",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("README missing sweep usage %q", want)
		}
	}
}

func sweepScriptPath(t *testing.T) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(scriptPath(t)))
	return filepath.Join(root, "scripts", "bench-wukongim-three-node-send-rate-sweep.sh")
}

func runSweepDryRun(t *testing.T, args ...string) (string, string) {
	t.Helper()
	outDir := t.TempDir()
	allArgs := append([]string{
		"--out-dir", outDir,
		"--dry-run",
		"--no-start-target",
		"--rates", "100",
		"--duration", "1s",
		"--expected-latency-ms", "200",
		"--inflight-multiplier", "2",
	}, args...)
	out, err := runBash(t, sweepScriptPath(t), allArgs...)
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	return outDir, out
}

func runBash(t *testing.T, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/bash", append([]string{script}, args...)...)
	cmd.Dir = filepath.Dir(filepath.Dir(scriptPath(t)))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func scriptPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return file
}
