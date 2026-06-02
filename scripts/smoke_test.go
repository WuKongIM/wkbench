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

func scriptPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return file
}
