# wkbench Remote Background Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add plugin RPC support for `contract.BackgroundUnit` and migrate `wukongim.metrics_collector/v1` into the official WuKongIM plugin without changing scenario YAML.

**Architecture:** Extend the existing stdio plugin protocol with `Start`, `Stop`, and background event frames, while keeping stream capability ports out of this phase. The host uses a single read pump to demultiplex synchronous RPC frames and asynchronous background events, and background manifests register a wrapper type that alone satisfies `contract.BackgroundUnit`.

**Tech Stack:** Go, protobuf, `benchkit/pluginhost`, `sdk/go/wkbench/plugin`, `benchkit/contract`, `cmd/wkbench`, official plugin binaries.

---

## Scope

Phase 2D-A proves remote background lifecycle using one official background unit:

- Implement remote `Start` and `Stop` lifecycle over stdio.
- Preserve outputs, aggregate metrics, and host-managed artifacts for background units.
- Surface plugin background failures through `BackgroundTask.Done`.
- Migrate `wukongim.metrics_collector/v1` from host-local registration into `plugins/official/wukongim`.

Keep these areas host-local in this phase:

- `traffic.*`
- `wkproto.session_pool/v1`
- fake senders
- `wukongim.prepare_tokens/v1`
- stream capability ports and one-RPC-per-message hot paths

## File Map

- `benchkit/protocol/wkbench_plugin.proto`: Add protocol messages and manifest background flag.
- `benchkit/protocol/wkbench_plugin.pb.go`: Regenerate from the proto file.
- `benchkit/pluginhost/manifest.go`: Add `Unit.Background` and clone it.
- `benchkit/pluginhost/stdio_client.go`: Add read pump, request waiters, background task registry, and `Start`/`Stop`.
- `benchkit/pluginhost/client.go`: Add `StartRequest` and extend the client interface.
- `benchkit/pluginhost/remote_unit.go`: Keep normal remote units non-background, add a background wrapper that implements `Start`.
- `benchkit/pluginhost/remote_unit_test.go`: Cover wrapper selection, delegation, and non-background behavior.
- `benchkit/pluginhost/stdio_client_test.go`: Cover manifest round trip, read pump behavior, remote background `Start`/`Stop`, fatal events, and non-background rejection.
- `sdk/go/wkbench/plugin/server.go`: Add write synchronization, task registry, `handleStart`, `handleStop`, shared run-env flushing helpers, and background event emission.
- `sdk/go/wkbench/plugin/server_test.go`: Cover manifest background flag, server start/stop lifecycle, stop flushing, fatal events, and non-background rejection.
- `plugins/official/wukongim/plugin.go`: Register `metricscollector.Unit{}` in the official WuKongIM plugin.
- `cmd/wkbench/main.go`: Remove host-local metrics collector registration and pass `Start` through `pendingLifecycleClient`.
- `cmd/wkbench/main_test.go`: Update default registration expectations and add a smoke scenario for remote metrics collection with a local HTTP metrics server.
- `docs/superpowers/specs/2026-06-04-wkbench-remote-background-lifecycle-design.md`: Leave as design record.
- Public docs that describe host-local versus official units, if present in `README.md` or `docs/*`: update only the affected lists.

## Safety Notes

- Do not add `Start` directly to `RemoteUnit`; that would make every remote unit satisfy `contract.BackgroundUnit` because Go uses structural interfaces.
- Add a separate `remoteBackgroundUnit` wrapper and return it only when the plugin manifest sets `Unit.Background`.
- After the read pump exists, no method in `StdioClient` may call `reader.ReadFrame` directly except the pump itself.
- All writes to plugin stdout in `sdk/go/wkbench/plugin/server.go` must pass through a synchronized helper because background monitor goroutines can write events while the main serve loop writes responses.
- Keep `GOWORK=off` on all Go commands.

---

### Task 1: Protocol and Manifest Background Flag

**Files:**
- Modify: `benchkit/protocol/wkbench_plugin.proto`
- Modify: `benchkit/protocol/wkbench_plugin.pb.go`
- Modify: `benchkit/pluginhost/manifest.go`
- Modify: `benchkit/pluginhost/stdio_client.go`
- Modify: `sdk/go/wkbench/plugin/server.go`
- Test: `sdk/go/wkbench/plugin/server_test.go`
- Test: `benchkit/pluginhost/stdio_client_test.go`

- [ ] **Step 1: Write the failing SDK manifest test**

Add this test to `sdk/go/wkbench/plugin/server_test.go` near the other manifest tests:

```go
func TestManifestFromUnitsMarksBackgroundUnit(t *testing.T) {
	manifest := ManifestFromUnits("test-plugin", "v1", []contract.Unit{
		backgroundManifestUnit{},
	})

	if got := len(manifest.Units); got != 1 {
		t.Fatalf("manifest unit count = %d, want 1", got)
	}
	if !manifest.Units[0].Background {
		t.Fatalf("manifest unit Background = false, want true")
	}
}

type backgroundManifestUnit struct{}

func (backgroundManifestUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.background_manifest/v1"}
}

func (backgroundManifestUnit) Validate(context.Context, contract.ValidateEnv) error {
	return nil
}

func (backgroundManifestUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}

func (backgroundManifestUnit) Run(context.Context, contract.RunEnv) error {
	return nil
}

func (backgroundManifestUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	return noopBackgroundTask{}, nil
}

type noopBackgroundTask struct{}

func (noopBackgroundTask) Stop(context.Context) error { return nil }
func (noopBackgroundTask) Done() <-chan error {
	ch := make(chan error)
	return ch
}
```

- [ ] **Step 2: Write the failing pluginhost manifest round-trip test**

Add this helper and test to `benchkit/pluginhost/stdio_client_test.go` near existing plugin build helpers and handshake tests:

```go
func buildSourcePlugin(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write plugin source: %v", err)
	}
	bin := filepath.Join(dir, "wkbench-source-plugin")
	build := exec.Command("go", "build", "-o", bin, sourcePath)
	build.Env = append(os.Environ(), "GOWORK=off")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build source plugin: %v\n%s", err, out)
	}
	return bin
}

func TestPluginManifestRoundTripsBackgroundUnit(t *testing.T) {
	pluginPath := buildSourcePlugin(t, `
package main

import (
	"context"
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

type backgroundUnit struct{}

func (backgroundUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.background_round_trip/v1"}
}

func (backgroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (backgroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (backgroundUnit) Run(context.Context, contract.RunEnv) error { return nil }
func (backgroundUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	return noopBackgroundTask{}, nil
}

type noopBackgroundTask struct{}

func (noopBackgroundTask) Stop(context.Context) error { return nil }
func (noopBackgroundTask) Done() <-chan error {
	ch := make(chan error)
	return ch
}

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name: "background-round-trip",
		Version: "dev",
		Units: []contract.Unit{backgroundUnit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`)

	client, err := StartStdioClient(context.Background(), pluginPath)
	if err != nil {
		t.Fatalf("start plugin: %v", err)
	}
	defer client.Close()

	manifest, err := client.Handshake(context.Background())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if got := len(manifest.Units); got != 1 {
		t.Fatalf("manifest unit count = %d, want 1", got)
	}
	if !manifest.Units[0].Background {
		t.Fatalf("manifest unit Background = false, want true")
	}
}
```

- [ ] **Step 3: Run the focused failing tests**

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -run TestManifestFromUnitsMarksBackgroundUnit -count=1
GOWORK=off go test ./benchkit/pluginhost -run TestPluginManifestRoundTripsBackgroundUnit -count=1
```

Expected: both fail to compile because `pluginhost.Unit.Background` and proto getters do not exist yet.

- [ ] **Step 4: Extend the proto**

Update `benchkit/protocol/wkbench_plugin.proto` so the `Frame` oneof includes:

```proto
    StartRequest start_request = 30;
    StartResponse start_response = 31;
    StopRequest stop_request = 32;
    StopResponse stop_response = 33;
    BackgroundEvent background_event = 34;
```

Update `UnitDefinition`:

```proto
message UnitDefinition {
  string kind = 1;
  string title = 2;
  string description = 3;
  repeated PortDef inputs = 4;
  repeated PortDef outputs = 5;
  repeated MetricDef metrics = 6;
  repeated ArtifactDef artifacts = 7;
  bool background = 8;
}
```

Add these messages after `RunRequest`:

```proto
message StartRequest {
  string unit_name = 1;
  string kind = 2;
  string run_id = 3;
  int64 run_duration_millis = 4;
  int32 worker_count = 5;
  bytes spec_json = 6;
  map<string, PortValue> inputs = 7;
}

message StartResponse {
  string task_id = 1;
}

message StopRequest {
  string task_id = 1;
}

message StopResponse {}

message BackgroundEvent {
  string task_id = 1;
  string event = 2;
  Error error = 3;
}
```

- [ ] **Step 5: Regenerate protocol Go code**

Run:

```bash
PATH="$(go env GOPATH)/bin:$PATH" /usr/local/include/protoc/bin/protoc --go_out=. --go_opt=paths=source_relative benchkit/protocol/wkbench_plugin.proto
```

Expected: `benchkit/protocol/wkbench_plugin.pb.go` is updated and contains `Frame_StartRequest`, `Frame_StopRequest`, `Frame_BackgroundEvent`, and `UnitDefinition.GetBackground`.

- [ ] **Step 6: Add manifest fields and conversions**

Update `benchkit/pluginhost/manifest.go`:

```go
type Unit struct {
	PluginName  string
	Kind        string
	Title       string
	Description string
	Inputs      []contract.PortDef
	Outputs     []contract.PortDef
	Metrics     []contract.MetricDef
	Artifacts   []contract.ArtifactDef
	Background  bool
}

func cloneUnit(u Unit) Unit {
	u.Inputs = clonePortDefs(u.Inputs)
	u.Outputs = clonePortDefs(u.Outputs)
	u.Metrics = slices.Clone(u.Metrics)
	u.Artifacts = slices.Clone(u.Artifacts)
	return u
}
```

Update `sdk/go/wkbench/plugin/server.go` in `ManifestFromUnits`:

```go
_, background := unit.(contract.BackgroundUnit)
out.Units = append(out.Units, pluginhost.Unit{
	Kind:        def.Kind,
	Title:       def.Title,
	Description: def.Description,
	Inputs:      clonePortDefs(def.Inputs),
	Outputs:     clonePortDefs(def.Outputs),
	Metrics:     slices.Clone(def.Metrics),
	Artifacts:   slices.Clone(def.Artifacts),
	Background:  background,
})
```

Update `unitToProto` in `sdk/go/wkbench/plugin/server.go`:

```go
func unitToProto(unit pluginhost.Unit) *protocol.UnitDefinition {
	return &protocol.UnitDefinition{
		Kind:        unit.Kind,
		Title:       unit.Title,
		Description: unit.Description,
		Inputs:      portsToProto(unit.Inputs),
		Outputs:     portsToProto(unit.Outputs),
		Metrics:     metricsToProto(unit.Metrics),
		Artifacts:   artifactsToProto(unit.Artifacts),
		Background:  unit.Background,
	}
}
```

Update `unitFromProto` in `benchkit/pluginhost/stdio_client.go`:

```go
return Unit{
	PluginName:  pluginName,
	Kind:        unit.GetKind(),
	Title:       unit.GetTitle(),
	Description: unit.GetDescription(),
	Inputs:      portsFromProto(unit.GetInputs()),
	Outputs:     portsFromProto(unit.GetOutputs()),
	Metrics:     metricsFromProto(unit.GetMetrics()),
	Artifacts:   artifactsFromProto(unit.GetArtifacts()),
	Background:  unit.GetBackground(),
}
```

- [ ] **Step 7: Run the manifest tests**

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -run TestManifestFromUnitsMarksBackgroundUnit -count=1
GOWORK=off go test ./benchkit/pluginhost -run TestPluginManifestRoundTripsBackgroundUnit -count=1
```

Expected: both pass.

- [ ] **Step 8: Commit protocol and manifest changes**

Run:

```bash
git add benchkit/protocol/wkbench_plugin.proto benchkit/protocol/wkbench_plugin.pb.go benchkit/pluginhost/manifest.go benchkit/pluginhost/stdio_client.go sdk/go/wkbench/plugin/server.go sdk/go/wkbench/plugin/server_test.go benchkit/pluginhost/stdio_client_test.go
git commit -m "feat: mark plugin background units"
```

---

### Task 2: StdioClient Read Pump

**Files:**
- Modify: `benchkit/pluginhost/stdio_client.go`
- Test: `benchkit/pluginhost/stdio_client_test.go`

- [ ] **Step 1: Add a failing cancellation/regression test for request waiters**

Add this test to `benchkit/pluginhost/stdio_client_test.go`:

```go
func TestStdioClientRequestWaiterSurvivesRepeatedCalls(t *testing.T) {
	pluginPath := buildSourcePlugin(t, `
package main

import (
	"context"
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

type unit struct{}

func (unit) Definition() contract.Definition { return contract.Definition{Kind: "test.waiter/v1"} }
func (unit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (unit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: "planned"}, nil
}
func (unit) Run(context.Context, contract.RunEnv) error { return nil }

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{Name: "waiter", Version: "dev", Units: []contract.Unit{unit{}}}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`)

	client, err := StartStdioClient(context.Background(), pluginPath)
	if err != nil {
		t.Fatalf("start plugin: %v", err)
	}
	defer client.Close()

	if _, err := client.Handshake(context.Background()); err != nil {
		t.Fatalf("handshake 1: %v", err)
	}
	if _, err := client.Handshake(context.Background()); err != nil {
		t.Fatalf("handshake 2: %v", err)
	}
	if err := client.Validate(context.Background(), UnitRequest{
		UnitName: "unit",
		Kind:     "test.waiter/v1",
		SpecJSON: []byte(`{}`),
	}); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
```

- [ ] **Step 2: Run the regression test before refactoring**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run TestStdioClientRequestWaiterSurvivesRepeatedCalls -count=1
```

Expected: pass on current code. This locks down existing synchronous behavior before the read pump rewrite.

- [ ] **Step 3: Add request waiter and read pump types**

Update `StdioClient` in `benchkit/pluginhost/stdio_client.go`:

```go
type StdioClient struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writer *protocol.FrameWriter

	writeMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
	waitOnce  sync.Once
	waitCh    chan error
	stateMu   sync.Mutex
	closed    bool
	killed    bool
	nextSeq   int64

	pumpDone chan error
	reqMu    sync.Mutex
	waiters  map[string]chan readResult
	bgTasks  map[string]*remoteBackgroundTask
}

type readResult struct {
	frame *protocol.Frame
	err   error
}
```

Replace the old `ioMu` with `writeMu`. Keep serialized writes, but move request reading out of lifecycle methods.

- [ ] **Step 4: Start the read pump in `StartStdioCommand`**

Update the client constructor:

```go
reader := protocol.NewFrameReader(stdout, 16<<20)
client := &StdioClient{
	cmd:      cmd,
	stdin:    stdin,
	writer:   protocol.NewFrameWriter(stdin),
	waitCh:   make(chan error, 1),
	pumpDone: make(chan error, 1),
	waiters:  make(map[string]chan readResult),
	bgTasks:  make(map[string]*remoteBackgroundTask),
}
go client.readLoop(reader)
return client, nil
```

Add the read loop:

```go
func (c *StdioClient) readLoop(reader *protocol.FrameReader) {
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			c.failAllWaiters(err)
			c.failAllBackgroundTasks(fmt.Errorf("read plugin frame: %w", err))
			c.pumpDone <- err
			return
		}
		if event := frame.GetBackgroundEvent(); event != nil {
			c.dispatchBackgroundEvent(event)
			continue
		}
		c.reqMu.Lock()
		waiter := c.waiters[frame.GetRequestId()]
		c.reqMu.Unlock()
		if waiter == nil {
			c.failAllBackgroundTasks(fmt.Errorf("unexpected plugin frame for request %q", frame.GetRequestId()))
			continue
		}
		waiter <- readResult{frame: frame}
	}
}
```

- [ ] **Step 5: Add waiter helpers**

Add helpers in `benchkit/pluginhost/stdio_client.go`:

```go
func (c *StdioClient) registerWaiter(requestID string) chan readResult {
	ch := make(chan readResult, 16)
	c.reqMu.Lock()
	c.waiters[requestID] = ch
	c.reqMu.Unlock()
	return ch
}

func (c *StdioClient) unregisterWaiter(requestID string) {
	c.reqMu.Lock()
	delete(c.waiters, requestID)
	c.reqMu.Unlock()
}

func (c *StdioClient) failAllWaiters(err error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	for requestID, waiter := range c.waiters {
		delete(c.waiters, requestID)
		waiter <- readResult{err: err}
		close(waiter)
	}
}

func (c *StdioClient) nextRequestID(prefix string) string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.nextSeq++
	return fmt.Sprintf("%s-%d", prefix, c.nextSeq)
}

func (c *StdioClient) writeFrame(frame *protocol.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() {
		return errStdioClientClosed
	}
	return c.writer.WriteFrame(frame)
}
```

- [ ] **Step 6: Replace direct reads in `Handshake`, `Validate`, and `Plan`**

Replace the old direct read pattern with:

```go
waiter := c.registerWaiter(requestID)
defer c.unregisterWaiter(requestID)
if err := c.writeFrame(frame); err != nil {
	return Plugin{}, fmt.Errorf("write handshake request: %w", err)
}
response, err := c.waitForRequestFrame(ctx, waiter, "handshake")
if err != nil {
	return Plugin{}, err
}
```

Add the shared waiter method:

```go
func (c *StdioClient) waitForRequestFrame(ctx context.Context, waiter <-chan readResult, op string) (*protocol.Frame, error) {
	select {
	case result := <-waiter:
		if result.err != nil {
			return nil, fmt.Errorf("read %s response: %w", op, result.err)
		}
		return result.frame, nil
	default:
	}

	select {
	case result := <-waiter:
		if result.err != nil {
			return nil, fmt.Errorf("read %s response: %w", op, result.err)
		}
		return result.frame, nil
	case <-ctx.Done():
		c.shutdown(true)
		c.startWait()
		return nil, fmt.Errorf("%s canceled: %w", op, ctx.Err())
	}
}
```

Keep request-id checks in each caller:

```go
if response.GetRequestId() != requestID {
	return Plugin{}, fmt.Errorf("handshake response request id = %q, want %q", response.GetRequestId(), requestID)
}
```

- [ ] **Step 7: Replace direct reads in `Run` and artifact acknowledgements**

In `Run`, register one waiter for `requestID`, write the `RunRequest`, then loop on `waitForRequestFrame`. Replace artifact error writes and artifact opened/closed writes with `c.writeFrame`:

```go
waiter := c.registerWaiter(requestID)
defer c.unregisterWaiter(requestID)
if err := c.writeFrame(runFrame); err != nil {
	return fmt.Errorf("write run request: %w", err)
}

for {
	frame, err := c.waitForRequestFrame(ctx, waiter, "run")
	if err != nil {
		return err
	}
	if frame.GetRequestId() != requestID {
		return fmt.Errorf("run response request id = %q, want %q", frame.GetRequestId(), requestID)
	}
	// Existing switch body remains, using c.writeFrame for host replies.
}
```

Change `writeRunArtifactError` to accept a write function:

```go
func writeRunArtifactError(write func(*protocol.Frame) error, requestID, runID, unitName string, err error) error {
	if writeErr := write(&protocol.Frame{
		RequestId:      requestID,
		RunId:          runID,
		UnitInstanceId: unitName,
		Body: &protocol.Frame_Error{Error: &protocol.Error{
			Code:    "ARTIFACT_ERROR",
			Message: err.Error(),
		}},
	}); writeErr != nil {
		return errors.Join(err, fmt.Errorf("write artifact error response: %w", writeErr))
	}
	return err
}
```

- [ ] **Step 8: Remove old read helpers**

Delete:

```go
func (c *StdioClient) readFrame(ctx context.Context, op string) (*protocol.Frame, error)
func waitForFrame(ctx context.Context, readResultCh <-chan readResult) (readResult, bool, error)
```

Keep `readResult`, because the read pump uses it.

- [ ] **Step 9: Update close behavior for the pump**

Update `close` so it waits for process exit and tolerates expected pump EOF after stdin close:

```go
func (c *StdioClient) close() error {
	c.shutdown(false)
	done := c.startWait()

	select {
	case err := <-done:
		if err != nil && !c.wasKilled() {
			return fmt.Errorf("wait plugin: %w", err)
		}
	case <-time.After(2 * time.Second):
		c.shutdown(true)
		err := <-done
		if err != nil && !c.wasKilled() {
			return fmt.Errorf("plugin did not exit after stdin close: %w", err)
		}
	}

	select {
	case <-c.pumpDone:
	default:
	}
	return nil
}
```

- [ ] **Step 10: Run pluginhost tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -count=1
```

Expected: pass.

- [ ] **Step 11: Commit read pump**

Run:

```bash
git add benchkit/pluginhost/stdio_client.go benchkit/pluginhost/stdio_client_test.go
git commit -m "feat: add stdio plugin read pump"
```

---

### Task 3: Host Remote Background Unit API

**Files:**
- Modify: `benchkit/pluginhost/client.go`
- Modify: `benchkit/pluginhost/remote_unit.go`
- Modify: `cmd/wkbench/main.go`
- Test: `benchkit/pluginhost/remote_unit_test.go`

- [ ] **Step 1: Write failing tests for wrapper selection**

Add these tests to `benchkit/pluginhost/remote_unit_test.go`:

```go
func TestNewRemoteUnitDoesNotImplementBackgroundWhenManifestDoesNotMarkIt(t *testing.T) {
	unit := NewRemoteUnit(&fakeClient{}, Unit{
		Kind: "test.normal/v1",
	})

	if _, ok := unit.(contract.BackgroundUnit); ok {
		t.Fatalf("non-background remote unit implements BackgroundUnit")
	}
}

func TestNewRemoteUnitImplementsBackgroundWhenManifestMarksIt(t *testing.T) {
	unit := NewRemoteUnit(&fakeClient{}, Unit{
		Kind:       "test.background/v1",
		Background: true,
	})

	if _, ok := unit.(contract.BackgroundUnit); !ok {
		t.Fatalf("background remote unit does not implement BackgroundUnit")
	}
}
```

Update existing tests that instantiate `NewRemoteUnit` or `NewRemoteUnitAlias` so the local variable type is `contract.Unit` or uses type assertions for remote-specific behavior. The constructor will return an interface after this task.

- [ ] **Step 2: Write failing `Start` delegation test**

Add this test to `benchkit/pluginhost/remote_unit_test.go`:

```go
func TestRemoteBackgroundUnitDelegatesStart(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnit(client, Unit{
		Kind:       "test.background_start/v1",
		Background: true,
		Inputs: []contract.PortDef{{
			Name: "input",
			Type: "test.input/v1",
			Meta: contract.PortMeta{
				Boundary:        contract.PortBoundaryData,
				Transport:       contract.PortTransportInline,
				Encodings:       []string{"json"},
				MaxPayloadBytes: 1024,
			},
		}},
	})
	background, ok := unit.(contract.BackgroundUnit)
	if !ok {
		t.Fatalf("remote unit did not implement BackgroundUnit")
	}
	env := contract.NewTestRunEnv("run-1", "background", map[string]any{"input": map[string]any{"ok": true}}, nil)

	task, err := background.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task == nil {
		t.Fatalf("start task is nil")
	}
	if client.startReq.Kind != "test.background_start/v1" {
		t.Fatalf("start kind = %q", client.startReq.Kind)
	}
	if _, ok := client.startReq.Inputs["input"]; !ok {
		t.Fatalf("start inputs missing input")
	}
}
```

Extend the existing `fakeClient` test double:

```go
type fakeClient struct {
	validateCalled bool
	planCalled     bool
	runCalled      bool
	startCalled    bool
	validateReq    UnitRequest
	planReq        UnitRequest
	runReq         RunRequest
	startReq       StartRequest
}

func (c *fakeClient) Start(ctx context.Context, req StartRequest, env contract.RunEnv) (contract.BackgroundTask, error) {
	c.startCalled = true
	c.startReq = req
	return noopBackgroundTask{}, nil
}

type noopBackgroundTask struct{}

func (noopBackgroundTask) Stop(context.Context) error { return nil }
func (noopBackgroundTask) Done() <-chan error {
	ch := make(chan error)
	return ch
}
```

- [ ] **Step 3: Run failing wrapper tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run 'TestNewRemoteUnit|TestRemoteBackgroundUnitDelegatesStart' -count=1
```

Expected: fail to compile because constructors return `RemoteUnit` and `Client.Start` does not exist.

- [ ] **Step 4: Extend the client contract**

Update `benchkit/pluginhost/client.go`:

```go
type Client interface {
	Validate(context.Context, UnitRequest) error
	Plan(context.Context, UnitRequest) (contract.Plan, error)
	Run(context.Context, RunRequest, contract.RunEnv) error
	Start(context.Context, StartRequest, contract.RunEnv) (contract.BackgroundTask, error)
}

type StartRequest struct {
	RunRequest
}
```

- [ ] **Step 5: Add the remote background wrapper**

Update `benchkit/pluginhost/remote_unit.go`:

```go
type remoteRunnable interface {
	contract.Unit
}

type RemoteUnit struct {
	client    Client
	unit      Unit
	aliasKind string
}

type remoteBackgroundUnit struct {
	RemoteUnit
}

func NewRemoteUnit(client Client, unit Unit) contract.Unit {
	base := RemoteUnit{client: client, unit: unit}
	if unit.Background {
		return remoteBackgroundUnit{RemoteUnit: base}
	}
	return base
}

func NewRemoteUnitAlias(client Client, unit Unit, aliasKind string) contract.Unit {
	base := RemoteUnit{client: client, unit: unit, aliasKind: aliasKind}
	if unit.Background {
		return remoteBackgroundUnit{RemoteUnit: base}
	}
	return base
}
```

Keep `Validate`, `Plan`, and `Run` methods on `RemoteUnit`.

- [ ] **Step 6: Implement background `Start` on the wrapper only**

Add to `benchkit/pluginhost/remote_unit.go`:

```go
func (u remoteBackgroundUnit) Start(ctx context.Context, env contract.RunEnv) (contract.BackgroundTask, error) {
	spec, err := encodeSpec(env)
	if err != nil {
		return nil, err
	}
	inputs, err := collectInputs(u.unit.Definition().Inputs, env)
	if err != nil {
		return nil, err
	}
	inputSourceDefs, err := collectInputSourceDefs(env, inputs)
	if err != nil {
		return nil, err
	}
	return u.client.Start(ctx, StartRequest{RunRequest: RunRequest{
		UnitRequest: UnitRequest{
			PluginName:        u.unit.PluginName,
			UnitName:          env.UnitName(),
			Kind:              u.unit.Kind,
			RunID:             env.RunID(),
			RunDurationMillis: env.RunDuration().Milliseconds(),
			WorkerCount:       env.WorkerCount(),
			SpecJSON:          spec,
		},
		InputDefs:       u.unit.Definition().Inputs,
		InputSourceDefs: inputSourceDefs,
		Inputs:          inputs,
	}}, env)
}
```

- [ ] **Step 7: Forward `Start` through the CLI lifecycle client**

Update `cmd/wkbench/main.go`:

```go
func (c pendingLifecycleClient) Start(ctx context.Context, req pluginhost.StartRequest, env contract.RunEnv) (contract.BackgroundTask, error) {
	return c.client.Start(ctx, req, env)
}
```

Registration calls can stay as:

```go
reg.Register(pluginhost.NewRemoteUnitAlias(remoteClient, unit, qualifiedKind))
reg.Register(pluginhost.NewRemoteUnit(remoteClient, unit))
```

because the registry accepts `contract.Unit`.

- [ ] **Step 8: Run remote unit tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run 'TestRemoteUnit|TestNewRemoteUnit|TestRemoteBackgroundUnit' -count=1
```

Expected: pass.

- [ ] **Step 9: Commit host background API**

Run:

```bash
git add benchkit/pluginhost/client.go benchkit/pluginhost/remote_unit.go benchkit/pluginhost/remote_unit_test.go cmd/wkbench/main.go
git commit -m "feat: expose remote background units selectively"
```

---

### Task 4: SDK Server Start and Stop Lifecycle

**Files:**
- Modify: `sdk/go/wkbench/plugin/server.go`
- Test: `sdk/go/wkbench/plugin/server_test.go`

- [ ] **Step 1: Write failing server start/stop test**

Add this test to `sdk/go/wkbench/plugin/server_test.go`:

```go
func TestServerStartStopBackgroundUnit(t *testing.T) {
	unit := &serverBackgroundUnit{}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	var out bytes.Buffer
	writer := protocol.NewFrameWriter(&out)
	startFrame := &protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}

	if err := srv.handleStart(context.Background(), startFrame, startFrame.GetStartRequest(), nil, writer); err != nil {
		t.Fatalf("handle start: %v", err)
	}

	response := readServerTestFrame(t, &out)
	taskID := response.GetStartResponse().GetTaskId()
	if taskID == "" {
		t.Fatalf("empty task id")
	}
	if unit.runCalled {
		t.Fatalf("Run was called for background start")
	}
	if !unit.startCalled {
		t.Fatalf("Start was not called")
	}

	out.Reset()
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	if err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), writer); err != nil {
		t.Fatalf("handle stop: %v", err)
	}
	stopResponse := readServerTestFrame(t, &out)
	if stopResponse.GetStopResponse() == nil {
		t.Fatalf("expected stop response, got %T", stopResponse.Body)
	}
	if !unit.stopCalled {
		t.Fatalf("Stop was not called")
	}
}
```

Add the test unit:

```go
type serverBackgroundUnit struct {
	startCalled bool
	runCalled   bool
	stopCalled  bool
}

func (u *serverBackgroundUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.server_background/v1"}
}

func (u *serverBackgroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (u *serverBackgroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (u *serverBackgroundUnit) Run(context.Context, contract.RunEnv) error {
	u.runCalled = true
	return nil
}
func (u *serverBackgroundUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	u.startCalled = true
	return serverBackgroundTask{unit: u}, nil
}

type serverBackgroundTask struct {
	unit *serverBackgroundUnit
}

func (t serverBackgroundTask) Stop(context.Context) error {
	t.unit.stopCalled = true
	return nil
}

func (serverBackgroundTask) Done() <-chan error {
	return make(chan error)
}
```

- [ ] **Step 2: Write failing non-background rejection test**

Add:

```go
func TestServerStartRejectsNonBackgroundUnit(t *testing.T) {
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{serverNormalUnit{}}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "start-normal",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "normal",
			Kind:     "test.server_normal/v1",
			SpecJson: []byte(`{}`),
		}},
	}
	if err := srv.handleStart(context.Background(), frame, frame.GetStartRequest(), nil, protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle start: %v", err)
	}
	response := readServerTestFrame(t, &out)

	rpcErr := response.GetError()
	if rpcErr == nil {
		t.Fatalf("expected error frame")
	}
	if rpcErr.GetCode() != "CONFIG_ERROR" {
		t.Fatalf("error code = %q, want CONFIG_ERROR", rpcErr.GetCode())
	}
}

type serverNormalUnit struct{}

func (serverNormalUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.server_normal/v1"}
}
func (serverNormalUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (serverNormalUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (serverNormalUnit) Run(context.Context, contract.RunEnv) error { return nil }
```

- [ ] **Step 3: Run failing server tests**

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -run 'TestServerStartStopBackgroundUnit|TestServerStartRejectsNonBackgroundUnit' -count=1
```

Expected: fail because `StartRequest` is unsupported.

- [ ] **Step 4: Add server write synchronization and task registry**

Update `server` in `sdk/go/wkbench/plugin/server.go`:

```go
type server struct {
	manifest    pluginhost.Plugin
	unitsByKind map[string]contract.Unit

	writeMu sync.Mutex
	taskMu  sync.Mutex
	nextTask int64
	tasks   map[string]*backgroundTaskRecord
}

type backgroundTaskRecord struct {
	id        string
	requestID string
	runID     string
	unitName  string
	unit      contract.Unit
	env       *remoteRunEnv
	task      contract.BackgroundTask
	stopping  bool
}
```

Initialize `tasks` in `newServer`:

```go
return &server{
	manifest:    ManifestFromUnits(plugin.Name, plugin.Version, plugin.Units),
	unitsByKind: unitsByKind,
	tasks:       make(map[string]*backgroundTaskRecord),
}
```

Add synchronized write helpers:

```go
func (s *server) writeFrame(writer *protocol.FrameWriter, frame *protocol.Frame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writer.WriteFrame(frame)
}

func (s *server) writeProtocolError(writer *protocol.FrameWriter, requestID, code, message string) error {
	return s.writeFrame(writer, &protocol.Frame{
		RequestId: requestID,
		Body: &protocol.Frame_Error{Error: &protocol.Error{
			Code:    code,
			Message: message,
		}},
	})
}
```

Convert existing `writeProtocolError(writer, ...)` calls to `s.writeProtocolError(writer, ...)`, or keep a top-level helper that delegates through a `frameWriter` interface. The important invariant is that all server writes use the same mutex.

- [ ] **Step 5: Route remote run-env writes through the same write lock**

Update `remoteRunEnv` in `sdk/go/wkbench/plugin/server.go` so artifact frames do not write directly to the shared stdout writer:

```go
type remoteRunEnv struct {
	*contract.TestRunEnv
	requestID    string
	runID        string
	unitName     string
	reader       *protocol.FrameReader
	writeFrame   func(*protocol.Frame) error
	artifactDefs map[string]contract.ArtifactDef

	ioMu sync.Mutex
}

func newRemoteRunEnv(requestID string, req *protocol.RunRequest, inputs map[string]any, spec map[string]any, artifacts []contract.ArtifactDef, reader *protocol.FrameReader, writeFrame func(*protocol.Frame) error) *remoteRunEnv {
	artifactDefs := make(map[string]contract.ArtifactDef, len(artifacts))
	for _, artifact := range artifacts {
		artifactDefs[artifact.Name] = artifact
	}
	return &remoteRunEnv{
		TestRunEnv:   contract.NewTestRunEnv(req.GetRunId(), req.GetUnitName(), inputs, spec),
		requestID:    requestID,
		runID:        req.GetRunId(),
		unitName:     req.GetUnitName(),
		reader:       reader,
		writeFrame:   writeFrame,
		artifactDefs: artifactDefs,
	}
}
```

In `OpenArtifact`, `writeArtifactChunk`, and `closeArtifact`, replace:

```go
e.writer.WriteFrame(frame)
```

with:

```go
e.writeFrame(frame)
```

Update all `newRemoteRunEnv` callers:

```go
env := newRemoteRunEnv(frame.GetRequestId(), req, inputs, spec, unit.Definition().Artifacts, reader, func(out *protocol.Frame) error {
	return s.writeFrame(writer, out)
})
```

Use the same pattern in `handleStart` when constructing the synthetic `RunRequest`.

- [ ] **Step 6: Add shared input/env and flush helpers**

Extract from `handleRun`:

```go
func decodeInputValues(values map[string]*protocol.PortValue) (map[string]any, error) {
	inputs := make(map[string]any, len(values))
	for name, value := range values {
		var decoded any
		if value != nil && len(value.GetPayload()) > 0 {
			if err := json.Unmarshal(value.GetPayload(), &decoded); err != nil {
				return nil, fmt.Errorf("decode input %q json: %w", name, err)
			}
		}
		inputs[name] = decoded
	}
	return inputs, nil
}

func configureRunEnv(env *remoteRunEnv, durationMillis int64, workerCount int32) {
	env.SetRunDuration(time.Duration(durationMillis) * time.Millisecond)
	if workerCount > 0 {
		env.SetWorkerCount(int(workerCount))
	}
}

func (s *server) writeOutputs(requestID string, env *remoteRunEnv, unit contract.Unit, writer *protocol.FrameWriter) error {
	for _, output := range unit.Definition().Outputs {
		value, ok := env.Output(output.Name)
		if !ok {
			continue
		}
		payload, err := encodeJSONPayload(value)
		if err != nil {
			return s.writeProtocolError(writer, requestID, "RUN_ERROR", err.Error())
		}
		var reportPayload []byte
		if output.Meta.Reportable && !output.Meta.Sensitive {
			reportValue := value
			if reportable, ok := value.(contract.ReportableOutput); ok {
				reportValue = reportable.ReportOutput()
			}
			reportPayload, err = encodeJSONPayload(reportValue)
			if err != nil {
				return s.writeProtocolError(writer, requestID, "RUN_ERROR", err.Error())
			}
		}
		if err := s.writeFrame(writer, &protocol.Frame{
			RequestId: requestID,
			Body: &protocol.Frame_SetOutput{SetOutput: &protocol.SetOutput{
				Name: output.Name,
				Value: &protocol.PortValue{
					Type:          string(output.Type),
					Encoding:      "json",
					Transport:     string(output.Meta.Transport),
					Sensitive:     output.Meta.Sensitive,
					Reportable:    output.Meta.Reportable,
					Payload:       payload,
					ReportPayload: reportPayload,
				},
			}},
		}); err != nil {
			return err
		}
	}
	return nil
}
```

Use `s.writeOutputs` and update `s.writeMetricFlush` so it calls `s.writeFrame`. Use both helpers from `handleRun` and `handleStop`.

- [ ] **Step 7: Implement `handleStart`**

Add a `StartRequest` case in `Serve`:

```go
case *protocol.Frame_StartRequest:
	if err := srv.handleStart(ctx, frame, frame.GetStartRequest(), reader, writer); err != nil {
		return err
	}
```

Add:

```go
func (s *server) handleStart(ctx context.Context, frame *protocol.Frame, req *protocol.StartRequest, reader *protocol.FrameReader, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.GetKind())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	background, ok := unit.(contract.BackgroundUnit)
	if !ok {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", fmt.Sprintf("unit kind %q is not a background unit", req.GetKind()))
	}
	spec, err := decodeSpecMap(req.GetSpecJson())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	inputs, err := decodeInputValues(req.GetInputs())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	runReq := &protocol.RunRequest{
		UnitName:          req.GetUnitName(),
		Kind:              req.GetKind(),
		RunId:             req.GetRunId(),
		RunDurationMillis: req.GetRunDurationMillis(),
		WorkerCount:       req.GetWorkerCount(),
		SpecJson:          req.GetSpecJson(),
		Inputs:            req.GetInputs(),
	}
	env := newRemoteRunEnv(frame.GetRequestId(), runReq, inputs, spec, unit.Definition().Artifacts, reader, func(out *protocol.Frame) error {
		return s.writeFrame(writer, out)
	})
	configureRunEnv(env, req.GetRunDurationMillis(), req.GetWorkerCount())
	task, err := background.Start(ctx, env)
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	record := s.storeBackgroundTask(frame.GetRequestId(), req.GetRunId(), req.GetUnitName(), unit, env, task)
	if err := s.writeFrame(writer, &protocol.Frame{
		RequestId: frame.GetRequestId(),
		RunId: req.GetRunId(),
		UnitInstanceId: req.GetUnitName(),
		Body: &protocol.Frame_StartResponse{StartResponse: &protocol.StartResponse{TaskId: record.id}},
	}); err != nil {
		return err
	}
	go s.monitorBackgroundTask(record.id, task, writer)
	return nil
}
```

Add:

```go
func (s *server) storeBackgroundTask(requestID, runID, unitName string, unit contract.Unit, env *remoteRunEnv, task contract.BackgroundTask) *backgroundTaskRecord {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	s.nextTask++
	id := fmt.Sprintf("bg-%d", s.nextTask)
	record := &backgroundTaskRecord{
		id:        id,
		requestID: requestID,
		runID:     runID,
		unitName:  unitName,
		unit:      unit,
		env:       env,
		task:      task,
	}
	s.tasks[id] = record
	return record
}
```

- [ ] **Step 8: Implement `handleStop`**

Add a `StopRequest` case in `Serve`:

```go
case *protocol.Frame_StopRequest:
	if err := srv.handleStop(ctx, frame, frame.GetStopRequest(), writer); err != nil {
		return err
	}
```

Add:

```go
func (s *server) handleStop(ctx context.Context, frame *protocol.Frame, req *protocol.StopRequest, writer *protocol.FrameWriter) error {
	record, ok := s.takeBackgroundTask(req.GetTaskId())
	if !ok {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", fmt.Sprintf("background task %q is not active", req.GetTaskId()))
	}
	if err := record.task.Stop(ctx); err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	if err := s.writeOutputs(record.requestID, record.env, record.unit, writer); err != nil {
		return err
	}
	if err := s.writeMetricFlush(record.requestID, record.env, writer); err != nil {
		return err
	}
	return s.writeFrame(writer, &protocol.Frame{
		RequestId: frame.GetRequestId(),
		RunId: record.runID,
		UnitInstanceId: record.unitName,
		Body: &protocol.Frame_StopResponse{StopResponse: &protocol.StopResponse{}},
	})
}

func (s *server) takeBackgroundTask(taskID string) (*backgroundTaskRecord, bool) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	record, ok := s.tasks[taskID]
	if !ok {
		return nil, false
	}
	record.stopping = true
	delete(s.tasks, taskID)
	return record, true
}
```

- [ ] **Step 9: Emit background events from task completion**

Add:

```go
func (s *server) monitorBackgroundTask(taskID string, task contract.BackgroundTask, writer *protocol.FrameWriter) {
	err, ok := <-task.Done()
	if !ok {
		err = nil
	}
	s.taskMu.Lock()
	record, active := s.tasks[taskID]
	if active && record.stopping {
		active = false
	}
	s.taskMu.Unlock()
	if !active {
		return
	}
	event := "completed"
	var rpcErr *protocol.Error
	if err != nil {
		event = "fatal_error"
		rpcErr = &protocol.Error{Code: "BACKGROUND_ERROR", Message: err.Error()}
	}
	_ = s.writeFrame(writer, &protocol.Frame{
		RequestId: record.requestID,
		RunId: record.runID,
		UnitInstanceId: record.unitName,
		Body: &protocol.Frame_BackgroundEvent{BackgroundEvent: &protocol.BackgroundEvent{
			TaskId: taskID,
			Event:  event,
			Error:  rpcErr,
		}},
	})
}
```

- [ ] **Step 10: Run server tests**

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -count=1
```

Expected: pass.

- [ ] **Step 11: Commit server lifecycle**

Run:

```bash
git add sdk/go/wkbench/plugin/server.go sdk/go/wkbench/plugin/server_test.go
git commit -m "feat: serve background lifecycle over plugin rpc"
```

---

### Task 5: StdioClient Remote Background Lifecycle

**Files:**
- Modify: `benchkit/pluginhost/stdio_client.go`
- Test: `benchkit/pluginhost/stdio_client_test.go`

- [ ] **Step 1: Write failing end-to-end Start/Stop test**

Add to `benchkit/pluginhost/stdio_client_test.go`:

```go
func TestStdioClientStartsAndStopsRemoteBackgroundUnit(t *testing.T) {
	pluginPath := buildSourcePlugin(t, `
package main

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

type backgroundUnit struct{}

func (backgroundUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.remote_background/v1",
		Outputs: []contract.PortDef{{
			Name: "summary",
			Type: "test.summary/v1",
			Meta: contract.PortMeta{
				Boundary:  contract.PortBoundaryData,
				Transport: contract.PortTransportInline,
				Reportable: true,
			},
		}},
		Metrics: []contract.MetricDef{{Name: "background_ticks", Type: "counter"}},
		Artifacts: []contract.ArtifactDef{{Name: "background.jsonl", ContentType: "application/jsonl"}},
	}
}
func (backgroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (backgroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (backgroundUnit) Run(context.Context, contract.RunEnv) error {
	return nil
}
func (backgroundUnit) Start(ctx context.Context, env contract.RunEnv) (contract.BackgroundTask, error) {
	writer, err := env.OpenArtifact("background.jsonl")
	if err != nil {
		return nil, err
	}
	task := &backgroundTask{env: env, writer: writer, wroteRunning: make(chan error, 1), done: make(chan error)}
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, err := writer.Write([]byte("{\"phase\":\"running\"}\n"))
		task.wroteRunning <- err
	}()
	return task, nil
}

type backgroundTask struct {
	env          contract.RunEnv
	writer       io.WriteCloser
	wroteRunning chan error
	done         chan error
}

func (t *backgroundTask) Stop(context.Context) error {
	if err := <-t.wroteRunning; err != nil {
		return err
	}
	t.env.EmitCounter("background_ticks", 2, nil)
	if _, err := t.writer.Write([]byte("{\"phase\":\"stop\"}\n")); err != nil {
		return err
	}
	if err := t.writer.Close(); err != nil {
		return err
	}
	if err := t.env.SetOutput("summary", map[string]any{"ok": true}); err != nil {
		return err
	}
	return nil
}
func (t *backgroundTask) Done() <-chan error { return t.done }

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{Name: "remote-background", Version: "dev", Units: []contract.Unit{backgroundUnit{}}}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`)

	client, err := StartStdioClient(context.Background(), pluginPath)
	if err != nil {
		t.Fatalf("start plugin: %v", err)
	}
	defer client.Close()
	manifest, err := client.Handshake(context.Background())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	unit := NewRemoteUnit(client, manifest.Units[0])
	background := unit.(contract.BackgroundUnit)
	env := contract.NewTestRunEnv("run-1", "background", nil, nil)
	env.DeclareArtifacts(manifest.Units[0].Artifacts)
	env.SetReportDir(t.TempDir())

	task, err := background.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := task.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, ok := env.Output("summary"); !ok {
		t.Fatalf("summary output missing")
	}
	sawMetric := false
	for _, snapshot := range env.MetricSnapshots() {
		if snapshot.Name == "background_ticks" {
			sawMetric = true
		}
	}
	if !sawMetric {
		t.Fatalf("background metric missing")
	}
	artifact := env.Artifacts()["background.jsonl"]
	data, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if got := string(data); got != "{\"phase\":\"running\"}\n{\"phase\":\"stop\"}\n" {
		t.Fatalf("artifact content = %q", got)
	}
}
```

- [ ] **Step 2: Write failing fatal event test**

Add:

```go
func TestStdioClientRemoteBackgroundFatalEventCompletesDone(t *testing.T) {
	pluginPath := buildSourcePlugin(t, `
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

type fatalBackgroundUnit struct{}

func (fatalBackgroundUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.remote_background_fatal/v1"}
}
func (fatalBackgroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (fatalBackgroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (fatalBackgroundUnit) Run(context.Context, contract.RunEnv) error { return nil }
func (fatalBackgroundUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	ch := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		ch <- fmt.Errorf("collector failed")
	}()
	return fatalTask{done: ch}, nil
}

type fatalTask struct{ done chan error }
func (fatalTask) Stop(context.Context) error { return nil }
func (t fatalTask) Done() <-chan error { return t.done }

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{Name: "fatal-background", Version: "dev", Units: []contract.Unit{fatalBackgroundUnit{}}}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`)

	client, err := StartStdioClient(context.Background(), pluginPath)
	if err != nil {
		t.Fatalf("start plugin: %v", err)
	}
	defer client.Close()
	manifest, err := client.Handshake(context.Background())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	background := NewRemoteUnit(client, manifest.Units[0]).(contract.BackgroundUnit)
	env := contract.NewTestRunEnv("run-1", "fatal-background", nil, nil)
	task, err := background.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case err := <-task.Done():
		if err == nil || !strings.Contains(err.Error(), "collector failed") {
			t.Fatalf("done error = %v, want collector failed", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for background fatal event")
	}
}
```

- [ ] **Step 3: Run failing lifecycle tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run 'TestStdioClientStartsAndStopsRemoteBackgroundUnit|TestStdioClientRemoteBackgroundFatalEventCompletesDone' -count=1
```

Expected: fail because `StdioClient.Start` is not implemented.

- [ ] **Step 4: Add host-side remote background task**

Add to `benchkit/pluginhost/stdio_client.go`:

```go
type remoteBackgroundTask struct {
	client    *StdioClient
	taskID    string
	requestID string
	runID     string
	unitName  string
	env       contract.RunEnv
	artifacts *runArtifactState
	done      chan error
	doneOnce  sync.Once
	stopOnce  sync.Once
	stopErr   error
}

func (t *remoteBackgroundTask) Stop(ctx context.Context) error {
	t.stopOnce.Do(func() {
		t.stopErr = t.client.stopBackgroundTask(ctx, t)
		t.client.unregisterBackgroundTask(t.taskID, t.requestID)
		t.artifacts.closeAll()
		if t.stopErr != nil {
			t.complete(t.stopErr)
			return
		}
		t.complete(nil)
	})
	return t.stopErr
}

func (t *remoteBackgroundTask) Done() <-chan error {
	return t.done
}

func (t *remoteBackgroundTask) complete(err error) {
	t.doneOnce.Do(func() {
		if err != nil {
			t.done <- err
		}
		close(t.done)
	})
}
```

- [ ] **Step 5: Add task registry and background event dispatch**

Extend the `StdioClient` struct created in Task 2 with a request-id index for active background units:

```go
bgTasks    map[string]*remoteBackgroundTask
bgRequests map[string]*remoteBackgroundTask
```

Initialize it in `StartStdioCommand`:

```go
bgTasks:    make(map[string]*remoteBackgroundTask),
bgRequests: make(map[string]*remoteBackgroundTask),
```

Add registry and dispatch helpers:

```go
func (c *StdioClient) registerBackgroundTask(task *remoteBackgroundTask) {
	c.reqMu.Lock()
	c.bgTasks[task.taskID] = task
	c.bgRequests[task.requestID] = task
	c.reqMu.Unlock()
}

func (c *StdioClient) unregisterBackgroundTask(taskID, requestID string) {
	c.reqMu.Lock()
	delete(c.bgTasks, taskID)
	delete(c.bgRequests, requestID)
	c.reqMu.Unlock()
}

func (c *StdioClient) dispatchBackgroundFrame(frame *protocol.Frame) bool {
	c.reqMu.Lock()
	task := c.bgRequests[frame.GetRequestId()]
	c.reqMu.Unlock()
	if task == nil {
		return false
	}
	if err := task.handleLifecycleFrame(frame); err != nil {
		task.complete(err)
	}
	return true
}

func (c *StdioClient) dispatchBackgroundEvent(event *protocol.BackgroundEvent) {
	c.reqMu.Lock()
	task := c.bgTasks[event.GetTaskId()]
	c.reqMu.Unlock()
	if task == nil {
		return
	}
	if event.GetEvent() == "fatal_error" {
		task.complete(pluginRPCError(event.GetError()))
		return
	}
	if event.GetEvent() == "completed" {
		task.complete(nil)
	}
}

func (c *StdioClient) failAllBackgroundTasks(err error) {
	c.reqMu.Lock()
	tasks := make([]*remoteBackgroundTask, 0, len(c.bgTasks))
	for taskID, task := range c.bgTasks {
		delete(c.bgTasks, taskID)
		delete(c.bgRequests, task.requestID)
		tasks = append(tasks, task)
	}
	c.reqMu.Unlock()
	for _, task := range tasks {
		task.artifacts.closeAll()
		task.complete(err)
	}
}
```

Update `readLoop` so non-event frames dispatch in this order:

```go
waiter := c.waiterForRequest(frame.GetRequestId())
if waiter != nil {
	waiter <- readResult{frame: frame}
	continue
}
if c.dispatchBackgroundFrame(frame) {
	continue
}
c.failAllBackgroundTasks(fmt.Errorf("unexpected plugin frame for request %q", frame.GetRequestId()))
```

Add this helper:

```go
func (c *StdioClient) waiterForRequest(requestID string) chan readResult {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	return c.waiters[requestID]
}
```

Add lifecycle frame handling on `remoteBackgroundTask`:

```go
func (t *remoteBackgroundTask) handleLifecycleFrame(frame *protocol.Frame) error {
	return handleRemoteLifecycleFrame(t.client.writeFrame, t.artifacts, t.env, t.requestID, t.runID, t.unitName, frame)
}

func handleRemoteLifecycleFrame(write func(*protocol.Frame) error, artifacts *runArtifactState, env contract.RunEnv, requestID, runID, unitName string, frame *protocol.Frame) error {
	switch body := frame.Body.(type) {
	case *protocol.Frame_SetOutput:
		return setOutputFromFrame(env, body.SetOutput)
	case *protocol.Frame_MetricFlush:
		applyMetricFlush(env, body.MetricFlush)
		return nil
	case *protocol.Frame_ArtifactOpen:
		opened, err := artifacts.open(body.ArtifactOpen)
		if err != nil {
			return writeRunArtifactError(write, requestID, runID, unitName, err)
		}
		return write(&protocol.Frame{
			RequestId:      requestID,
			RunId:          runID,
			UnitInstanceId: unitName,
			Body:           &protocol.Frame_ArtifactOpened{ArtifactOpened: opened},
		})
	case *protocol.Frame_ArtifactChunk:
		if err := artifacts.write(body.ArtifactChunk); err != nil {
			return writeRunArtifactError(write, requestID, runID, unitName, err)
		}
		return nil
	case *protocol.Frame_ArtifactClose:
		closed, err := artifacts.close(body.ArtifactClose)
		if err != nil {
			return writeRunArtifactError(write, requestID, runID, unitName, err)
		}
		return write(&protocol.Frame{
			RequestId:      requestID,
			RunId:          runID,
			UnitInstanceId: unitName,
			Body:           &protocol.Frame_ArtifactClosed{ArtifactClosed: closed},
		})
	default:
		return fmt.Errorf("unexpected background lifecycle frame %T", frame.Body)
	}
}
```

- [ ] **Step 6: Implement `Start`**

Add:

```go
func (c *StdioClient) Start(ctx context.Context, req StartRequest, env contract.RunEnv) (contract.BackgroundTask, error) {
	if err := ctx.Err(); err != nil {
		c.shutdown(true)
		c.startWait()
		return nil, err
	}
	inputs, err := encodeInputPortValues(req.InputDefs, req.InputSourceDefs, req.Inputs)
	if err != nil {
		return nil, err
	}
	requestID := c.nextRequestID("start")
	waiter := c.registerWaiter(requestID)
	defer c.unregisterWaiter(requestID)
	artifacts := newRunArtifactState(env)
	if err := c.writeFrame(&protocol.Frame{
		RequestId:      requestID,
		RunId:          req.RunID,
		UnitInstanceId: req.UnitName,
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName:          req.UnitName,
			Kind:              req.Kind,
			RunId:             req.RunID,
			RunDurationMillis: req.RunDurationMillis,
			WorkerCount:       int32(req.WorkerCount),
			SpecJson:          req.SpecJSON,
			Inputs:            inputs,
		}},
	}); err != nil {
		artifacts.closeAll()
		return nil, fmt.Errorf("write start request: %w", err)
	}
	for {
		frame, err := c.waitForRequestFrame(ctx, waiter, "start")
		if err != nil {
			artifacts.closeAll()
			return nil, err
		}
		if rpcErr := frame.GetError(); rpcErr != nil {
			artifacts.closeAll()
			return nil, pluginRPCError(rpcErr)
		}
		if response := frame.GetStartResponse(); response != nil {
			task := &remoteBackgroundTask{
				client:    c,
				taskID:    response.GetTaskId(),
				requestID: requestID,
				runID:     req.RunID,
				unitName:  req.UnitName,
				env:       env,
				artifacts: artifacts,
				done:      make(chan error, 1),
			}
			c.registerBackgroundTask(task)
			return task, nil
		}
		if err := handleRemoteLifecycleFrame(c.writeFrame, artifacts, env, requestID, req.RunID, req.UnitName, frame); err != nil {
			artifacts.closeAll()
			return nil, err
		}
	}
}
```

- [ ] **Step 7: Implement `Stop` frame handling**

Add:

```go
func (c *StdioClient) stopBackgroundTask(ctx context.Context, task *remoteBackgroundTask) error {
	requestID := c.nextRequestID("stop")
	waiter := c.registerWaiter(requestID)
	defer c.unregisterWaiter(requestID)
	if err := c.writeFrame(&protocol.Frame{
		RequestId:      requestID,
		RunId:          task.runID,
		UnitInstanceId: task.unitName,
		Body: &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{
			TaskId: task.taskID,
		}},
	}); err != nil {
		return fmt.Errorf("write stop request: %w", err)
	}
	for {
		frame, err := c.waitForRequestFrame(ctx, waiter, "stop")
		if err != nil {
			return err
		}
		switch body := frame.Body.(type) {
		case *protocol.Frame_StopResponse:
			return nil
		case *protocol.Frame_Error:
			return pluginRPCError(body.Error)
		default:
			return fmt.Errorf("unexpected stop response frame %T; background lifecycle frames must use start request id %q", body, task.requestID)
		}
	}
}
```

- [ ] **Step 8: Run lifecycle tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run 'TestStdioClientStartsAndStopsRemoteBackgroundUnit|TestStdioClientRemoteBackgroundFatalEventCompletesDone' -count=1
```

Expected: pass.

- [ ] **Step 9: Run all pluginhost tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -count=1
```

Expected: pass.

- [ ] **Step 10: Commit host lifecycle**

Run:

```bash
git add benchkit/pluginhost/stdio_client.go benchkit/pluginhost/stdio_client_test.go
git commit -m "feat: run remote background lifecycle from host"
```

---

### Task 6: Official Metrics Collector Migration

**Files:**
- Modify: `plugins/official/wukongim/plugin.go`
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`
- Modify: docs that list official or host-local units, only when present

- [ ] **Step 1: Write failing official plugin manifest test**

In `cmd/wkbench/main_test.go`, update the official WuKongIM plugin expectation so it includes:

```go
"wukongim.metrics_collector/v1",
```

The existing test names may differ; use `rg -n "prepare_group_channels|metrics_collector|official wukongim" cmd/wkbench/main_test.go` to find the current list.

- [ ] **Step 2: Write failing no-official-plugins expectation**

Update the `-no-official-plugins list-units` test so it asserts:

```go
if strings.Contains(stdout.String(), "wukongim.metrics_collector/v1") {
	t.Fatalf("no-official-plugins list-units unexpectedly contained metrics collector:\n%s", stdout.String())
}
```

Keep expectations for host-local fake senders, traffic units, `wkproto.session_pool/v1`, and `wukongim.prepare_tokens/v1`.

- [ ] **Step 3: Add a remote metrics collector smoke test**

Merge these imports into the existing `cmd/wkbench/main_test.go` import block:

```go
import (
	"io"
	"net/http"
	"net/http/httptest"
)
```

Add a focused smoke test in `cmd/wkbench/main_test.go`:

```go
func TestRunRemoteMetricsCollectorWithLocalMetricsServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, "wk_active_conn_count 7\n")
	}))
	t.Cleanup(server.Close)

	scenario := filepath.Join(t.TempDir(), "remote-metrics.yaml")
	reportDir := filepath.Join(t.TempDir(), "report")
	if err := os.WriteFile(scenario, []byte(fmt.Sprintf(`
version: wkbench/v2
run:
  id: remote-metrics-smoke
  duration: 120ms
  report_dir: %q
units:
  target:
    use: wukongim.target
    spec:
      api_addrs: [%q]
      gateway_tcp_addrs: ["127.0.0.1:0"]
      bench_api_token: ""
      operation_timeout: 50ms
      skip_readiness: true
      skip_capabilities: true
  metrics:
    use: wukongim.metrics_collector
    after: [target]
    inputs:
      target: target.target
    spec:
      interval: 20ms
      timeout: 50ms
      path: /metrics
      include: ["wk_active_conn_count"]
  traffic:
    use: core.fake_message_sender
    after: [metrics]
`, reportDir, server.URL)), 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"run", "-scenario", scenario}, &stderr)
	if code != exitOK {
		t.Fatalf("run exit code = %d, stderr:\n%s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(reportDir, "artifacts", "metrics", "metrics.jsonl"))
	if err != nil {
		t.Fatalf("read metrics artifact: %v", err)
	}
	if !bytes.Contains(data, []byte("wk_active_conn_count")) {
		t.Fatalf("metrics artifact missing scraped metric:\n%s", string(data))
	}
}
```

- [ ] **Step 4: Run failing CLI tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'Test.*Official|TestRunRemoteMetricsCollectorWithLocalMetricsServer|Test.*NoOfficial' -count=1
```

Expected: fail because metrics collector is still host-local and missing from the official WuKongIM plugin.

- [ ] **Step 5: Move metrics collector into official WuKongIM plugin**

Update `plugins/official/wukongim/plugin.go` imports:

```go
import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	metricscollector "github.com/WuKongIM/wkbench/units/wukongim/metrics_collector"
	preparegroups "github.com/WuKongIM/wkbench/units/wukongim/prepare_group_channels"
	wukongtarget "github.com/WuKongIM/wkbench/units/wukongim/target"
)
```

Update units:

```go
Units: []contract.Unit{
	wukongtarget.Unit{},
	preparegroups.Unit{},
	metricscollector.Unit{},
},
```

Update `cmd/wkbench/main.go` imports by removing:

```go
metricscollector "github.com/WuKongIM/wkbench/units/wukongim/metrics_collector"
```

Remove this call from `defaultRegistry`:

```go
metricscollector.Register(reg)
```

- [ ] **Step 6: Update docs that list local and official units**

Run:

```bash
rg -n "metrics_collector|official plugin|official.*wukongim|host-local|host local" README.md docs -g'*.md'
```

For any matching list, update it to state:

```markdown
- Official WuKongIM plugin: `wukongim.target/v1`, `wukongim.prepare_group_channels/v1`, `wukongim.metrics_collector/v1`
- Host-local units: fake senders, `traffic.*`, `wkproto.session_pool/v1`, `wukongim.prepare_tokens/v1`
```

Do not edit unrelated prose.

- [ ] **Step 7: Run CLI and official plugin tests**

Run:

```bash
GOWORK=off go test ./plugins/official/wukongim ./cmd/wkbench -count=1
```

Expected: pass.

- [ ] **Step 8: Commit migration**

Run:

```bash
git add plugins/official/wukongim/plugin.go cmd/wkbench/main.go cmd/wkbench/main_test.go README.md docs
git commit -m "feat: run metrics collector as official plugin"
```

If docs did not change, omit `README.md docs` from `git add`.

---

### Task 7: Full Verification and Advanced Review

**Files:**
- Review: all files changed on `codex/remote-background-lifecycle`

- [ ] **Step 1: Run focused package tests**

Run:

```bash
GOWORK=off go test ./benchkit/protocol ./benchkit/pluginhost ./sdk/go/wkbench/plugin ./plugins/official/wukongim ./cmd/wkbench -count=1
GOWORK=off go test ./units/wukongim/metrics_collector -count=1
```

Expected: pass.

- [ ] **Step 2: Validate representative scenarios**

Run:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/group-send.yaml
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/wukongim-send-rate-with-metrics.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./examples/wukongim-send-rate-with-metrics.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./examples/wukongim-send-rate-with-metrics.yaml
```

Expected: each command exits 0.

- [ ] **Step 3: Run the full suite**

Run:

```bash
GOWORK=off go test ./...
```

Expected: pass.

- [ ] **Step 4: Inspect changed files**

Run:

```bash
git diff --stat main...HEAD
git diff --check main...HEAD
git status --short
```

Expected: `git diff --check` prints no output. `git status --short` is clean after the final verification commit.

- [ ] **Step 5: Request advanced subagent review**

Dispatch a high-capability review subagent with this prompt:

```text
Review the wkbench remote background lifecycle branch for correctness, race risks, protocol compatibility, and test gaps.

Focus areas:
- StdioClient read pump has exactly one reader and no remaining direct reader.ReadFrame calls outside the pump.
- RemoteUnit itself does not implement contract.BackgroundUnit; only the background wrapper does.
- SDK server writes are synchronized and background monitor goroutines cannot race normal responses.
- Start/Stop preserves outputs, metrics, and artifacts for background units.
- Fatal BackgroundEvent paths complete the host task Done channel with an error.
- cmd/wkbench no-official-plugins behavior leaves metrics_collector unavailable, while default official plugin loading still exposes it.

Return only blocking or important findings with file and line references. Include test commands you would add if you find coverage gaps.
```

- [ ] **Step 6: Apply accepted review fixes**

For each valid review finding, make a minimal code change and run the narrowest test covering it. Use this commit message template:

```bash
git add <changed-files>
git commit -m "fix: address remote background review"
```

If the review returns no blocking or important findings, make no code changes.

- [ ] **Step 7: Final branch status**

Run:

```bash
git status --short
git log --oneline --max-count=8
```

Expected: status is clean and the latest commits show this phase's implementation commits.

---

## Self Review

- Spec coverage: Tasks 1 through 5 implement protocol, manifest, host API, SDK server lifecycle, read pump, stop flushing, and fatal background events. Task 6 migrates `wukongim.metrics_collector/v1` into the official plugin. Task 7 covers verification and advanced review.
- Type consistency: `StartRequest` wraps `RunRequest`; proto `StartRequest` mirrors proto `RunRequest`; `remoteBackgroundUnit` alone implements `Start`; `StdioClient.Start` returns `contract.BackgroundTask`.
- Boundary consistency: stream capability ports remain outside this phase; inline input validation continues through `encodeInputPortValues`.
- Review note: the execution agent should revisit test helper names while implementing because the test files already contain several helpers. Keep behavior and assertions from this plan even if helper names are adapted to existing local patterns.
