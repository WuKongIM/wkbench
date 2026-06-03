# wkbench Phase 2A Artifact Streaming And Data Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let remote plugin `Run` write host-managed artifacts, make JSON data ports concrete across the RPC boundary, and ship the first pure-data official plugin binaries.

**Architecture:** Extend the plugin protobuf with explicit artifact frames handled by the host while a remote `Run` is active. The Go SDK provides a plugin-side `RunEnv` whose `OpenArtifact` returns a writer that streams chunks to the host, while outputs and aggregate metrics keep their existing terminal flush behavior. Concrete DTOs in `benchkit/ports/*` become the boundary-safe data shapes used by pure-data units and official plugin binaries.

**Tech Stack:** Go 1.23, protobuf `google.golang.org/protobuf`, stdio framed plugin protocol, existing `benchkit/kernel` RunEnv artifact writer, existing `sdk/go/wkbench/plugin`.

---

## File Structure

- Modify `benchkit/protocol/wkbench_plugin.proto`: add explicit artifact frame messages to the `Frame` oneof.
- Regenerate `benchkit/protocol/wkbench_plugin.pb.go` with `protoc`.
- Modify `benchkit/pluginhost/stdio_client.go`: handle artifact open/chunk/close frames during `Run`.
- Modify `benchkit/pluginhost/stdio_client_test.go`: add focused host artifact frame tests.
- Modify `sdk/go/wkbench/plugin/server.go`: replace `TestRunEnv` in `Run` with a remote env that streams artifact writes.
- Modify `sdk/go/wkbench/plugin/server_test.go`: add SDK tests proving artifact frames are emitted during `Run`.
- Modify `benchkit/ports/channel/*.go` and `benchkit/ports/identity/*.go`: add concrete JSON DTOs and metadata helpers.
- Modify pure-data unit packages under `units/core`, `units/identity`, and `units/report`: use concrete DTOs and explicit port metadata.
- Create `plugins/official/dataplane/cmd/wkbench-official-data-plugin/main.go`: official pure-data plugin binary.
- Create `plugins/official/dataplane/plugin_test.go`: verify manifest contains the migrated pure-data units.
- Modify docs and examples: document remote artifact streaming and the official data plugin.

## Task 1: Add Artifact Protocol Frames

**Files:**
- Modify: `benchkit/protocol/wkbench_plugin.proto`
- Modify generated: `benchkit/protocol/wkbench_plugin.pb.go`
- Test: `benchkit/protocol/frame_test.go`

- [ ] **Step 1: Write the failing protocol test**

Append this test to `benchkit/protocol/frame_test.go`:

```go
func TestFrameRoundTripArtifactChunk(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)
	want := &Frame{
		RequestId:      "run-1",
		RunId:          "scenario-1",
		UnitInstanceId: "collector",
		Body: &Frame_ArtifactChunk{ArtifactChunk: &ArtifactChunk{
			Handle:   "artifact-1",
			Sequence: 2,
			Data:     []byte("payload"),
		}},
	}

	if err := writer.WriteFrame(want); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got, err := NewFrameReader(&buf, 1024).ReadFrame()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	chunk := got.GetArtifactChunk()
	if chunk == nil {
		t.Fatalf("artifact chunk frame missing: %#v", got)
	}
	if chunk.GetHandle() != "artifact-1" || chunk.GetSequence() != 2 || string(chunk.GetData()) != "payload" {
		t.Fatalf("unexpected chunk: %#v", chunk)
	}
}
```

- [ ] **Step 2: Run the protocol test and verify RED**

Run:

```bash
GOWORK=off go test ./benchkit/protocol -run TestFrameRoundTripArtifactChunk -count=1
```

Expected: fail to compile because `Frame_ArtifactChunk` and `ArtifactChunk` do not exist.

- [ ] **Step 3: Add artifact messages to the proto**

In `benchkit/protocol/wkbench_plugin.proto`, add these oneof members after `SetOutput`:

```proto
    ArtifactOpen artifact_open = 25;
    ArtifactOpened artifact_opened = 26;
    ArtifactChunk artifact_chunk = 27;
    ArtifactClose artifact_close = 28;
    ArtifactClosed artifact_closed = 29;
```

Add these messages after `SetOutput`:

```proto
message ArtifactOpen {
  string name = 1;
}

message ArtifactOpened {
  string name = 1;
  string handle = 2;
}

message ArtifactChunk {
  string handle = 1;
  int64 sequence = 2;
  bytes data = 3;
}

message ArtifactClose {
  string handle = 1;
}

message ArtifactClosed {
  string handle = 1;
  int64 size_bytes = 2;
}
```

- [ ] **Step 4: Regenerate protobuf Go code**

Run:

```bash
protoc --go_out=. --go_opt=module=github.com/WuKongIM/wkbench benchkit/protocol/wkbench_plugin.proto
```

Expected: `benchkit/protocol/wkbench_plugin.pb.go` contains generated `ArtifactOpen`, `ArtifactChunk`, and related oneof wrappers.

- [ ] **Step 5: Run protocol tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./benchkit/protocol -run Artifact -count=1
```

Expected: PASS.

## Task 2: Handle Remote Artifact Frames In The Host

**Files:**
- Modify: `benchkit/pluginhost/stdio_client.go`
- Test: `benchkit/pluginhost/stdio_client_test.go`

- [ ] **Step 1: Write failing host tests**

Add tests to `benchkit/pluginhost/stdio_client_test.go` that exercise helper functions without starting a process:

```go
func TestArtifactFrameHandlerWritesHostArtifact(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "remote", nil, nil)
	env.DeclareArtifacts([]contract.ArtifactDef{{Name: "metrics.jsonl", ContentType: "application/jsonl"}})
	env.SetReportDir(t.TempDir())
	state := newRunArtifactState(env)

	opened, err := state.open(&protocol.ArtifactOpen{Name: "metrics.jsonl"})
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	if opened.GetHandle() == "" || opened.GetName() != "metrics.jsonl" {
		t.Fatalf("unexpected opened response: %#v", opened)
	}
	if err := state.write(&protocol.ArtifactChunk{Handle: opened.GetHandle(), Sequence: 1, Data: []byte("{\"ok\":true}\n")}); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	closed, err := state.close(&protocol.ArtifactClose{Handle: opened.GetHandle()})
	if err != nil {
		t.Fatalf("close artifact: %v", err)
	}
	if closed.GetSizeBytes() != int64(len("{\"ok\":true}\n")) {
		t.Fatalf("size = %d", closed.GetSizeBytes())
	}
	info := env.Artifacts()["metrics.jsonl"]
	if info.ContentType != "application/jsonl" || info.SizeBytes != closed.GetSizeBytes() {
		t.Fatalf("artifact info = %#v", info)
	}
	data, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(data) != "{\"ok\":true}\n" {
		t.Fatalf("artifact payload = %q", data)
	}
}

func TestArtifactFrameHandlerRejectsUnknownHandle(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "remote", nil, nil)
	state := newRunArtifactState(env)

	err := state.write(&protocol.ArtifactChunk{Handle: "missing", Sequence: 1, Data: []byte("x")})
	if err == nil || !strings.Contains(err.Error(), "unknown artifact handle") {
		t.Fatalf("error = %v, want unknown handle", err)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run 'TestArtifactFrameHandler' -count=1
```

Expected: fail to compile because `SetReportDir`, `newRunArtifactState`, `open`, `write`, and `close` are missing.

- [ ] **Step 3: Add test RunEnv report directory support**

In `benchkit/contract/types.go`, add a `reportDir string` field to `TestRunEnv`, add:

```go
// SetReportDir sets the directory used by OpenArtifact in tests.
func (e *TestRunEnv) SetReportDir(dir string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reportDir = dir
}
```

Update `TestRunEnv.OpenArtifact` so it writes into `reportDir/artifacts/<unit>/<name>` when `reportDir` is set, preserving the existing temp-file fallback when it is empty.

- [ ] **Step 4: Implement host artifact frame state**

In `benchkit/pluginhost/stdio_client.go`, add a private state type:

```go
type runArtifactState struct {
	env     contract.RunEnv
	next    int64
	writers map[string]*hostArtifactWriter
}

type hostArtifactWriter struct {
	name   string
	writer io.WriteCloser
	size   int64
}
```

Implement `newRunArtifactState(env contract.RunEnv)`, `open(*protocol.ArtifactOpen)`, `write(*protocol.ArtifactChunk)`, `close(*protocol.ArtifactClose)`, and `closeAll()`.

`open` must call `env.OpenArtifact(name)` and return a generated handle like `artifact-1`. `write` must reject unknown handles and write bytes immediately. `close` must close and delete the handle, then return the accumulated byte size. `closeAll` must close any leaked writer during terminal error paths.

- [ ] **Step 5: Wire artifact frames into `StdioClient.Run`**

In the `Run` read loop, initialize `artifacts := newRunArtifactState(env)` after sending `RunRequest`, defer `artifacts.closeAll()`, and handle:

```go
case *protocol.Frame_ArtifactOpen:
	opened, err := artifacts.open(body.ArtifactOpen)
	if err != nil {
		return err
	}
	if err := c.writer.WriteFrame(&protocol.Frame{
		RequestId:      requestID,
		RunId:          req.RunID,
		UnitInstanceId: req.UnitName,
		Body:          &protocol.Frame_ArtifactOpened{ArtifactOpened: opened},
	}); err != nil {
		return fmt.Errorf("write artifact opened response: %w", err)
	}
case *protocol.Frame_ArtifactChunk:
	if err := artifacts.write(body.ArtifactChunk); err != nil {
		return err
	}
case *protocol.Frame_ArtifactClose:
	closed, err := artifacts.close(body.ArtifactClose)
	if err != nil {
		return err
	}
	if err := c.writer.WriteFrame(&protocol.Frame{
		RequestId:      requestID,
		RunId:          req.RunID,
		UnitInstanceId: req.UnitName,
		Body:          &protocol.Frame_ArtifactClosed{ArtifactClosed: closed},
	}); err != nil {
		return fmt.Errorf("write artifact closed response: %w", err)
	}
```

- [ ] **Step 6: Run host tests**

Run:

```bash
GOWORK=off go test ./benchkit/contract ./benchkit/pluginhost -run 'TestTestRunEnvWritesDeclaredArtifact|TestArtifactFrameHandler|TestStdioClient' -count=1
```

Expected: PASS.

## Task 3: Stream Artifacts From The Go SDK During Run

**Files:**
- Modify: `sdk/go/wkbench/plugin/server.go`
- Test: `sdk/go/wkbench/plugin/server_test.go`

- [ ] **Step 1: Write failing SDK artifact test**

Add a unit in `server_test.go` whose `Run` opens `metrics.jsonl`, writes two chunks, closes it, and sets a reportable output. Add a test that sends a `RunRequest`, reads frames from the server, responds to `ArtifactOpen` with `ArtifactOpened`, responds to `ArtifactClose` with `ArtifactClosed`, and asserts the frame order includes open, chunks, close, output, terminal status.

The essential assertion should be:

```go
if got := string(chunks.Bytes()); got != "one\ntwo\n" {
	t.Fatalf("artifact chunks = %q", got)
}
```

- [ ] **Step 2: Run SDK test and verify RED**

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -run TestServerRunStreamsArtifacts -count=1
```

Expected: FAIL because plugin `OpenArtifact` still uses `TestRunEnv` and does not emit artifact frames.

- [ ] **Step 3: Add a remote run environment**

In `sdk/go/wkbench/plugin/server.go`, add `remoteRunEnv` implementing `contract.RunEnv`, `contract.MetricSnapshotRecorder`, and `contract.OutputReader`. It should keep existing spec/input/output/metric behavior from `TestRunEnv`, but implement `OpenArtifact` by returning a writer that talks to the host through `protocol.FrameWriter` and `protocol.FrameReader`.

The artifact writer must:

```go
type remoteArtifactWriter struct {
	env      *remoteRunEnv
	name     string
	handle   string
	sequence int64
	closed   bool
}
```

`OpenArtifact` sends `ArtifactOpen{Name: name}`, reads `ArtifactOpened`, stores the returned handle, and returns the writer. `Write` chunks data into bounded slices no larger than `64 << 10`, sends `ArtifactChunk`, and returns the original byte count. `Close` sends `ArtifactClose`, waits for `ArtifactClosed`, and prevents duplicate close.

- [ ] **Step 4: Use remote run env in `handleRun`**

Replace:

```go
env := contract.NewTestRunEnv(req.GetRunId(), req.GetUnitName(), inputs, spec)
```

with a `newRemoteRunEnv(req, inputs, spec, writer, s.unit(req.GetKind()).Definition().Artifacts)` constructor. Preserve run duration, worker count, outputs, and metrics behavior. Keep `handleValidate` and `handlePlan` on `TestRunEnv`.

- [ ] **Step 5: Run SDK tests**

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -run 'TestServerRunStreamsArtifacts|TestServerRunSendsMetricFlush|TestServerRunSeparatesRawAndReportPayload' -count=1
```

Expected: PASS.

## Task 4: Make Data Ports Concrete Across RPC

**Files:**
- Modify: `benchkit/ports/channel/group_set.go`
- Modify: `benchkit/ports/channel/send_target.go`
- Modify: `benchkit/ports/identity/identity.go`
- Modify: relevant tests under `benchkit/ports/*`
- Modify: pure-data units that produce or consume these data ports.

- [ ] **Step 1: Write failing port DTO tests**

Add tests proving JSON can round-trip into concrete DTOs and still satisfy the existing interfaces:

```go
func TestGroupSetDataJSONRoundTripImplementsGroupSet(t *testing.T) {
	source := channelport.GroupSetData{Items: []channelport.GroupChannel{{ChannelID: "g1", Members: []string{"u1"}}}}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded channelport.GroupSetData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var set channelport.GroupSet = decoded
	if set.Count() != 1 || set.At(0).ChannelID != "g1" {
		t.Fatalf("decoded set = %#v", decoded)
	}
}
```

Repeat the same shape for `SendTargetSetData` and `identity.PoolData`.

- [ ] **Step 2: Run port tests and verify RED**

Run:

```bash
GOWORK=off go test ./benchkit/ports/channel ./benchkit/ports/identity -run 'DataJSONRoundTrip' -count=1
```

Expected: fail because DTO types are missing.

- [ ] **Step 3: Add concrete DTOs**

Add:

```go
type GroupSetData struct {
	Items []GroupChannel `json:"items"`
}

func (s GroupSetData) Count() int { return len(s.Items) }
func (s GroupSetData) At(index int) GroupChannel { return s.Items[index] }
```

Add:

```go
type SendTargetSetData struct {
	Items []SendTarget `json:"items"`
}

func (s SendTargetSetData) Count() int { return len(s.Items) }
func (s SendTargetSetData) At(index int) SendTarget { return s.Items[index] }
```

Add:

```go
type PoolData struct {
	Items []Identity `json:"items"`
}

func (p PoolData) Count() int { return len(p.Items) }
func (p PoolData) At(index int) Identity { return p.Items[index] }
func (p PoolData) TokenFor(uid string) (string, bool) {
	for _, item := range p.Items {
		if item.UID == uid {
			return item.Token, item.Token != ""
		}
	}
	return "", false
}
```

- [ ] **Step 4: Update pure-data units to emit DTOs**

Change `units/core/static_groups` to output `channel.GroupSetData`. Change `units/identity/pool` to output `identity.PoolData`. Change `units/identity/person_pairs` to output `channel.SendTargetSetData`.

Keep compatibility type aliases if tests or docs refer to package-local `GroupSet`, `Pool`, or `TargetSet`:

```go
type GroupSet = channelport.GroupSetData
type Pool = identityport.PoolData
type TargetSet = channelport.SendTargetSetData
```

- [ ] **Step 5: Mark pure data ports as inline JSON data**

Set explicit metadata on pure-data inputs and outputs:

```go
Meta: contract.PortMeta{
	Boundary:        contract.PortBoundaryData,
	Transport:       contract.PortTransportInline,
	Encodings:       []string{"json"},
	MaxPayloadBytes: contract.DefaultInlinePortMaxPayloadBytes,
}
```

For reportable `traffic.Summary` and assertion results, also set `Reportable: true` on outputs that should appear in reports.

- [ ] **Step 6: Run focused tests**

Run:

```bash
GOWORK=off go test ./benchkit/ports/channel ./benchkit/ports/identity ./units/core/static_groups ./units/identity/pool ./units/identity/person_pairs ./units/report/assert -count=1
```

Expected: PASS.

## Task 5: Add The First Official Data Plugin

**Files:**
- Create: `plugins/official/dataplane/cmd/wkbench-official-data-plugin/main.go`
- Create: `plugins/official/dataplane/plugin_test.go`
- Modify: `docs/plugin-authoring.md`
- Create: `examples/official-data-plugin.yaml`

- [ ] **Step 1: Write failing official plugin test**

Create `plugins/official/dataplane/plugin_test.go`:

```go
package data_test

import (
	"testing"

	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
	personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
	identitypool "github.com/WuKongIM/wkbench/units/identity/pool"
	assertunit "github.com/WuKongIM/wkbench/units/report/assert"
)

func TestOfficialDataManifestContainsPureDataUnits(t *testing.T) {
	manifest := plugin.ManifestFromUnits("wkbench.official.data", "dev", []contract.Unit{
		identitypool.Unit{},
		personpairs.Unit{},
		staticgroups.Unit{},
		assertunit.Unit{},
	})
	kinds := map[string]bool{}
	for _, unit := range manifest.Units {
		kinds[unit.Kind] = true
	}
	for _, kind := range []string{"identity.pool/v1", "identity.person_pairs/v1", "core.static_groups/v1", "report.assert/v1"} {
		if !kinds[kind] {
			t.Fatalf("manifest missing %s", kind)
		}
	}
}
```

Import `contract` in the final test code.

- [ ] **Step 2: Run test and verify RED**

Run:

```bash
GOWORK=off go test ./plugins/official/dataplane -count=1
```

Expected: fail because the package does not exist.

- [ ] **Step 3: Add official data plugin binary**

Create `plugins/official/dataplane/cmd/wkbench-official-data-plugin/main.go`:

```go
package main

import (
	"log"
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
	personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
	identitypool "github.com/WuKongIM/wkbench/units/identity/pool"
	assertunit "github.com/WuKongIM/wkbench/units/report/assert"
)

func main() {
	if err := plugin.Serve(plugin.Plugin{
		Name:    "wkbench.official.data",
		Version: "dev",
		Units: []contract.Unit{
			identitypool.Unit{},
			personpairs.Unit{},
			staticgroups.Unit{},
			assertunit.Unit{},
		},
	}, os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 4: Add manifest test helper**

Make `plugins/official/dataplane/plugin_test.go` compile with the same unit list as the binary. Keep the test in package `dataplane_test` so it verifies public package imports only.

- [ ] **Step 5: Add example scenario**

Create `examples/official-data-plugin.yaml`:

```yaml
version: wkbench/v2

run:
  id: official-data-plugin-demo
  duration: 1s
  report_dir: ./reports/official-data-plugin-demo

units:
  identities:
    use: wkbench.official.data:identity.pool/v1
    spec:
      total: 4

  pairs:
    use: wkbench.official.data:identity.person_pairs/v1
    inputs:
      identities: identities.pool
    spec:
      count: 3
      mode: ring
```

- [ ] **Step 6: Document artifact streaming and official data plugin**

In `docs/plugin-authoring.md`, remove the sentence saying remote plugin artifact streaming is unsupported. Add:

```markdown
Remote plugin `Run` supports host-managed artifact writes through `env.OpenArtifact`.
Artifacts must be declared in `Definition.Artifacts`; the host writes them
under `run.report_dir/artifacts/<unit>/<artifact-name>` and records metadata in
`report.json`.
```

Also add build/run commands for the official data plugin:

```bash
GOWORK=off go build -o /tmp/wkbench-official-data-plugin ./plugins/official/dataplane/cmd/wkbench-official-data-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-official-data-plugin validate -scenario ./examples/official-data-plugin.yaml
```

- [ ] **Step 7: Run official plugin tests and scenario validation**

Run:

```bash
GOWORK=off go test ./plugins/official/dataplane ./cmd/wkbench -count=1
GOWORK=off go build -o /tmp/wkbench-official-data-plugin ./plugins/official/dataplane/cmd/wkbench-official-data-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-official-data-plugin validate -scenario ./examples/official-data-plugin.yaml
```

Expected: PASS and scenario validates using plugin-qualified unit kinds.

## Task 6: End-To-End Artifact Streaming Verification

**Files:**
- Modify: `plugins/demo/echo/unit.go`
- Modify: `plugins/demo/echo/unit_test.go`
- Modify: `examples/plugin-echo.yaml`

- [ ] **Step 1: Add failing demo artifact test**

Add a test to `plugins/demo/echo/unit_test.go` proving `demo.echo/v1` declares and writes `echo.json` when run with `OpenArtifact`.

- [ ] **Step 2: Run test and verify RED**

Run:

```bash
GOWORK=off go test ./plugins/demo/echo -run Artifact -count=1
```

Expected: fail because the demo unit does not declare or write an artifact.

- [ ] **Step 3: Update demo unit**

Add `Artifacts: []contract.ArtifactDef{{Name: "echo.json", ContentType: "application/json"}}` to the demo definition. In `Run`, after setting the output, open `echo.json`, write `{"message":"..."}` JSON, and close it.

- [ ] **Step 4: Run plugin scenario and inspect report**

Run:

```bash
GOWORK=off go test ./plugins/demo/echo ./sdk/go/wkbench/plugin ./benchkit/pluginhost -count=1
GOWORK=off go build -o /tmp/wkbench-demo-plugin ./plugins/demo/cmd/wkbench-demo-plugin
rm -rf ./reports/plugin-echo
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin run -scenario ./examples/plugin-echo.yaml
test -f ./reports/plugin-echo/artifacts/echo/echo.json
```

Expected: PASS; `echo.json` exists under the host report directory.

## Task 7: Full Verification

**Files:**
- All changed files.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
GOWORK=off go test ./benchkit/protocol ./benchkit/contract ./benchkit/pluginhost ./sdk/go/wkbench/plugin ./benchkit/ports/channel ./benchkit/ports/identity ./plugins/demo/echo ./plugins/official/dataplane ./cmd/wkbench -count=1
```

Expected: PASS.

- [ ] **Step 2: Run all tests**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run scenario validations**

Run:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/group-send.yaml
GOWORK=off go build -o /tmp/wkbench-demo-plugin ./plugins/demo/cmd/wkbench-demo-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin validate -scenario ./examples/plugin-echo.yaml
GOWORK=off go build -o /tmp/wkbench-official-data-plugin ./plugins/official/dataplane/cmd/wkbench-official-data-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-official-data-plugin validate -scenario ./examples/official-data-plugin.yaml
```

Expected: all commands exit 0.

- [ ] **Step 4: Check git status**

Run:

```bash
git status --short
```

Expected: only intentional Phase 2A changes are present.

## Self-Review

- Spec coverage: artifact streaming is covered by Tasks 1, 2, 3, and 6; concrete data ports are covered by Task 4; first official data plugin is covered by Task 5; full verification is covered by Task 7.
- Placeholder scan: no `TBD`, `TODO`, or unresolved "implement later" markers are present.
- Type consistency: artifact proto types are named consistently as `ArtifactOpen`, `ArtifactOpened`, `ArtifactChunk`, `ArtifactClose`, and `ArtifactClosed`; DTO names are consistently `GroupSetData`, `SendTargetSetData`, and `PoolData`.
