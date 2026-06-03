package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

func TestStdioClientListsDemoPluginUnits(t *testing.T) {
	bin := buildDemoPlugin(t)

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	manifest, err := client.Handshake(context.Background())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if manifest.Name != "wkbench.demo" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	if len(manifest.Units) != 1 || manifest.Units[0].Kind != "demo.echo/v1" {
		t.Fatalf("units = %#v", manifest.Units)
	}
}

func TestStdioClientValidatePlanAndRunDemoPlugin(t *testing.T) {
	bin := buildDemoPlugin(t)

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	if _, err := client.Handshake(context.Background()); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	req := UnitRequest{
		PluginName:        "wkbench.demo",
		UnitName:          "echo",
		Kind:              "demo.echo/v1",
		RunID:             "run-1",
		RunDurationMillis: 1000,
		WorkerCount:       1,
		SpecJSON:          []byte(`{"message":"hello from stdio"}`),
	}
	if err := client.Validate(context.Background(), req); err != nil {
		t.Fatalf("validate: %v", err)
	}
	plan, err := client.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.UnitName != "echo" {
		t.Fatalf("plan UnitName = %q", plan.UnitName)
	}

	env := contract.NewTestRunEnv(req.RunID, req.UnitName, nil, nil)
	if err := client.Run(context.Background(), RunRequest{UnitRequest: req}, env); err != nil {
		t.Fatalf("run: %v", err)
	}
	output, ok := env.Output("result")
	if !ok {
		t.Fatal("missing result output")
	}
	reportable, ok := output.(contract.ReportableOutput)
	if !ok {
		t.Fatalf("result type = %T, want contract.ReportableOutput", output)
	}
	result, ok := reportable.ReportOutput().(map[string]any)
	if !ok {
		t.Fatalf("report output type = %T, want map[string]any", reportable.ReportOutput())
	}
	if result["message"] != "hello from stdio" {
		t.Fatalf("result message = %#v", result["message"])
	}
}

func TestRemoteReportableOutputReportsAndMarshalsWrappedValue(t *testing.T) {
	output := remoteReportableOutput{value: map[string]any{"message": "hello"}}
	reported, ok := output.ReportOutput().(map[string]any)
	if !ok {
		t.Fatalf("report output type = %T, want map[string]any", output.ReportOutput())
	}
	if reported["message"] != "hello" {
		t.Fatalf("reported message = %#v", reported["message"])
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if string(data) != `{"message":"hello"}` {
		t.Fatalf("json = %s", data)
	}
}

func TestSetOutputFromFrameKeepsNonReportableOutputPlain(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "unit", nil, nil)

	if err := setOutputFromFrame(env, &protocol.SetOutput{
		Name: "result",
		Value: &protocol.PortValue{
			Encoding: "json",
			Payload:  []byte(`{"message":"hidden"}`),
		},
	}); err != nil {
		t.Fatalf("set output: %v", err)
	}

	output, ok := env.Output("result")
	if !ok {
		t.Fatal("missing result output")
	}
	if _, ok := output.(contract.ReportableOutput); ok {
		t.Fatalf("non-reportable output implements ReportableOutput: %T", output)
	}
	result, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("output type = %T, want map[string]any", output)
	}
	if result["message"] != "hidden" {
		t.Fatalf("message = %#v", result["message"])
	}
}

func TestSetOutputFromFrameDoesNotExposeSensitiveReportableOutput(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "unit", nil, nil)

	if err := setOutputFromFrame(env, &protocol.SetOutput{
		Name: "secret",
		Value: &protocol.PortValue{
			Encoding:   "json",
			Reportable: true,
			Sensitive:  true,
			Payload:    []byte(`{"token":"secret"}`),
		},
	}); err != nil {
		t.Fatalf("set output: %v", err)
	}

	output, ok := env.Output("secret")
	if !ok {
		t.Fatal("missing secret output")
	}
	if _, ok := output.(contract.ReportableOutput); ok {
		t.Fatalf("sensitive output implements ReportableOutput: %T", output)
	}
}

func TestStdioClientCanceledHandshakeStopsHungPlugin(t *testing.T) {
	bin := writeSleepPlugin(t, 2*time.Second)

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = client.Handshake(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("handshake returned after %s, want prompt cancellation", elapsed)
	}

	closeStart := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	if elapsed := time.Since(closeStart); elapsed > time.Second {
		t.Fatalf("close returned after %s, want prompt close", elapsed)
	}
}

func TestStdioClientSuccessfulHandshakeStopsCancelWatcher(t *testing.T) {
	bin := buildDemoPlugin(t)

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	for i := 0; i < 20; i++ {
		ctx := newErrCancelContext()
		if _, err := client.Handshake(ctx); err != nil {
			t.Fatalf("handshake %d: %v", i, err)
		}

		time.Sleep(time.Millisecond)
		if _, err := client.Handshake(context.Background()); err != nil {
			t.Fatalf("follow-up handshake %d: %v", i, err)
		}
	}
}

func buildDemoPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "wkbench-demo-plugin")
	build := exec.Command("go", "build", "-o", bin, "./plugins/demo/cmd/wkbench-demo-plugin")
	build.Env = append(os.Environ(), "GOWORK=off")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}
	return bin
}

func writeSleepPlugin(t *testing.T, delay time.Duration) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wkbench-sleep-plugin")
	seconds := int(delay / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	body := "#!/bin/sh\nsleep " + strconv.Itoa(seconds) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write sleep plugin: %v", err)
	}
	return path
}

type errCancelContext struct {
	done  chan struct{}
	calls int
}

func newErrCancelContext() *errCancelContext {
	return &errCancelContext{done: make(chan struct{})}
}

func (c *errCancelContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (c *errCancelContext) Done() <-chan struct{} {
	return c.done
}

func (c *errCancelContext) Err() error {
	c.calls++
	if c.calls == 2 {
		close(c.done)
	}
	return nil
}

func (c *errCancelContext) Value(key any) any {
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}
