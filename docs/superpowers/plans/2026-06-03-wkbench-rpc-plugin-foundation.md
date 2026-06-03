# wkbench RPC Plugin Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first working RPC plugin foundation so `wkbench` can discover, validate, plan, and run a unit provided by an external plugin process.

**Architecture:** Keep the current kernel usable during migration by adapting remote plugin units into `contract.Unit` proxies, while moving the new host/plugin ABI into `benchkit/protocol`, `benchkit/pluginhost`, and `sdk/go/wkbench/plugin`. Use a single bidirectional length-prefixed protobuf frame stream for runtime calls, and require port metadata so the host can validate data, capability, local-resource, sensitivity, and paging rules before execution.

**Tech Stack:** Go 1.23, protobuf over length-prefixed stdio, standard-library process management, current `benchkit/contract`, current `benchkit/kernel`, `GOWORK=off go test`.

---

## Scope

This is Phase 1. It produces a testable plugin execution path and a demo plugin. It does not migrate every official unit yet.

Phase 1 success means:

- plugin protocol types exist and are tested;
- plugin port metadata exists and is validated by tests;
- Go SDK can serve a simple plugin;
- `pluginhost` can run a plugin process and call `Handshake`, `ListUnits`, `Validate`, `Plan`, and `Run`;
- remote plugin units can be registered as `contract.Unit` proxies in the existing kernel;
- CLI can load plugin executables through a global `-plugin` flag;
- a demo scenario using only the external demo plugin validates and runs.

Follow-up plans should migrate official plugins and remove in-process unit registration after this foundation is stable.

## File Structure

- Create `benchkit/protocol/wkbench_plugin.proto`: stable protobuf ABI for plugin frames, lifecycle messages, port values, metrics, and errors.
- Create `benchkit/protocol/frame.go`: length-prefixed protobuf frame reader/writer.
- Create `benchkit/protocol/frame_test.go`: codec tests for partial reads, multiple frames, and malformed frame sizes.
- Modify `benchkit/contract/types.go`: add port boundary, transport, sensitivity, and reportability metadata to `PortDef`.
- Modify `benchkit/contract/types_test.go`: cover default metadata and compile-time construction.
- Create `benchkit/pluginhost/manifest.go`: plugin manifest and resolved plugin metadata types.
- Create `benchkit/pluginhost/catalog.go`: catalog resolution for plugin unit definitions and duplicate kind rules.
- Create `benchkit/pluginhost/catalog_test.go`: duplicate and explicit plugin ownership tests.
- Create `benchkit/pluginhost/client.go`: client interface used by remote unit proxies.
- Create `benchkit/pluginhost/stdio_client.go`: process-backed plugin client over stdio frames.
- Create `benchkit/pluginhost/remote_unit.go`: adapter from plugin RPC lifecycle to `contract.Unit`.
- Create `benchkit/pluginhost/remote_unit_test.go`: fake-client tests proving validate, plan, run, output, and metrics behavior.
- Create `sdk/go/wkbench/plugin/server.go`: Go SDK server that adapts `contract.Unit` implementations to protocol frames.
- Create `sdk/go/wkbench/plugin/server_test.go`: in-memory server tests.
- Create `plugins/demo/echo/unit.go`: demo external unit for plugin smoke tests.
- Create `plugins/demo/echo/unit_test.go`: focused unit tests.
- Create `plugins/demo/cmd/wkbench-demo-plugin/main.go`: external plugin executable.
- Modify `cmd/wkbench/main.go`: parse global `-plugin` flags and register remote plugin units alongside existing built-ins during Phase 1.
- Modify `cmd/wkbench/main_test.go`: CLI tests for loading the demo plugin.
- Create `examples/plugin-echo.yaml`: demo scenario using an external plugin unit.
- Modify `README.md`: document Phase 1 plugin usage.

## Task 1: Add Port Boundary Metadata

**Files:**
- Modify: `benchkit/contract/types.go`
- Modify: `benchkit/contract/types_test.go`

- [ ] **Step 1: Write failing metadata defaults test**

Add this test to `benchkit/contract/types_test.go`:

```go
func TestPortDefDefaultsToInlineDataMetadata(t *testing.T) {
	port := contract.PortDef{Name: "summary", Type: "port.traffic.summary/v1"}
	meta := port.Metadata()
	if meta.Boundary != contract.PortBoundaryData {
		t.Fatalf("Boundary = %q, want %q", meta.Boundary, contract.PortBoundaryData)
	}
	if meta.Transport != contract.PortTransportInline {
		t.Fatalf("Transport = %q, want %q", meta.Transport, contract.PortTransportInline)
	}
	if meta.MaxPayloadBytes != contract.DefaultInlinePortMaxPayloadBytes {
		t.Fatalf("MaxPayloadBytes = %d, want %d", meta.MaxPayloadBytes, contract.DefaultInlinePortMaxPayloadBytes)
	}
	if meta.Sensitive {
		t.Fatal("Sensitive default must be false")
	}
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
GOWORK=off go test ./benchkit/contract -run TestPortDefDefaultsToInlineDataMetadata -count=1
```

Expected: FAIL because `PortDef.Metadata`, `PortBoundaryData`, `PortTransportInline`, and `DefaultInlinePortMaxPayloadBytes` do not exist.

- [ ] **Step 3: Add metadata types**

In `benchkit/contract/types.go`, add these declarations near `PortType`:

```go
// PortBoundary describes whether a port may cross plugin process boundaries.
type PortBoundary string

const (
	// PortBoundaryData is a serializable value that may cross plugins.
	PortBoundaryData PortBoundary = "data"
	// PortBoundaryStreamCapability is a remote behavior exposed through a stream.
	PortBoundaryStreamCapability PortBoundary = "stream_capability"
	// PortBoundaryLocalResource is plugin-local and cannot cross plugins.
	PortBoundaryLocalResource PortBoundary = "local_resource"
)

// PortTransport describes how a data port payload is carried.
type PortTransport string

const (
	// PortTransportInline carries one bounded payload.
	PortTransportInline PortTransport = "inline"
	// PortTransportPaged carries deterministic pages over the plugin stream.
	PortTransportPaged PortTransport = "paged"
	// PortTransportArtifactRef points at a host-managed artifact.
	PortTransportArtifactRef PortTransport = "artifact_ref"
)

const (
	// DefaultInlinePortMaxPayloadBytes bounds inline plugin data ports.
	DefaultInlinePortMaxPayloadBytes int64 = 1 << 20
	// DefaultReportableOutputMaxBytes bounds reportable output summaries.
	DefaultReportableOutputMaxBytes int64 = 64 << 10
)

// PortMeta describes plugin boundary metadata for one input or output port.
type PortMeta struct {
	Boundary        PortBoundary
	Transport       PortTransport
	Schema          string
	Encodings       []string
	MaxPayloadBytes int64
	Sensitive       bool
	Reportable      bool
	Operations      []string
}
```

Extend `PortDef`:

```go
type PortDef struct {
	// Name is the unit-local port name.
	Name string
	// Type is the versioned public port type.
	Type PortType
	// Optional allows an input port to be omitted.
	Optional bool
	// Meta describes plugin boundary behavior for this port.
	Meta PortMeta
}
```

Add this method:

```go
// Metadata returns port metadata with safe defaults applied.
func (p PortDef) Metadata() PortMeta {
	meta := p.Meta
	if meta.Boundary == "" {
		meta.Boundary = PortBoundaryData
	}
	if meta.Transport == "" {
		meta.Transport = PortTransportInline
	}
	if meta.MaxPayloadBytes == 0 {
		meta.MaxPayloadBytes = DefaultInlinePortMaxPayloadBytes
	}
	return meta
}
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
GOWORK=off go test ./benchkit/contract -run TestPortDefDefaultsToInlineDataMetadata -count=1
```

Expected: PASS.

- [ ] **Step 5: Add explicit capability metadata test**

Add this test:

```go
func TestPortDefMetadataPreservesExplicitCapability(t *testing.T) {
	port := contract.PortDef{
		Name: "sender",
		Type: "port.wkproto.message_sender/v1",
		Meta: contract.PortMeta{
			Boundary:   contract.PortBoundaryStreamCapability,
			Schema:     "wkbench.ports.wkproto.MessageSenderV1",
			Encodings:  []string{"protobuf"},
			Reportable: false,
			Operations: []string{"OpenSendStream"},
		},
	}
	meta := port.Metadata()
	if meta.Boundary != contract.PortBoundaryStreamCapability {
		t.Fatalf("Boundary = %q", meta.Boundary)
	}
	if meta.Operations[0] != "OpenSendStream" {
		t.Fatalf("Operations = %#v", meta.Operations)
	}
}
```

- [ ] **Step 6: Run contract package tests**

Run:

```bash
GOWORK=off go test ./benchkit/contract
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add benchkit/contract/types.go benchkit/contract/types_test.go
git commit -m "feat: add plugin port metadata"
```

## Task 2: Add Protobuf Protocol Schema

**Files:**
- Create: `benchkit/protocol/wkbench_plugin.proto`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Create protocol schema**

Create `benchkit/protocol/wkbench_plugin.proto`:

```proto
syntax = "proto3";

package wkbench.plugin.v1;

option go_package = "github.com/WuKongIM/wkbench/benchkit/protocol;protocol";

message Frame {
  string request_id = 1;
  string run_id = 2;
  string unit_instance_id = 3;
  oneof body {
    HandshakeRequest handshake_request = 10;
    HandshakeResponse handshake_response = 11;
    ListUnitsRequest list_units_request = 12;
    ListUnitsResponse list_units_response = 13;
    ValidateRequest validate_request = 14;
    ValidateResponse validate_response = 15;
    PlanRequest plan_request = 16;
    PlanResponse plan_response = 17;
    RunRequest run_request = 18;
    RunEnvRequest run_env_request = 19;
    RunEnvResponse run_env_response = 20;
    MetricFlush metric_flush = 21;
    SetOutput set_output = 22;
    TerminalStatus terminal_status = 23;
    Error error = 24;
  }
}

message HandshakeRequest {
  string host_protocol = 1;
  string min_protocol = 2;
  string max_protocol = 3;
}

message HandshakeResponse {
  PluginManifest manifest = 1;
  string selected_protocol = 2;
}

message PluginManifest {
  string name = 1;
  string version = 2;
  string protocol = 3;
  string source = 4;
  string checksum = 5;
  repeated UnitDefinition units = 6;
}

message ListUnitsRequest {}

message ListUnitsResponse {
  repeated UnitDefinition units = 1;
}

message UnitDefinition {
  string kind = 1;
  string title = 2;
  string description = 3;
  repeated PortDef inputs = 4;
  repeated PortDef outputs = 5;
  repeated MetricDef metrics = 6;
  repeated ArtifactDef artifacts = 7;
}

message PortDef {
  string name = 1;
  string type = 2;
  bool optional = 3;
  PortMeta meta = 4;
}

message PortMeta {
  string boundary = 1;
  string transport = 2;
  string schema = 3;
  repeated string encodings = 4;
  int64 max_payload_bytes = 5;
  bool sensitive = 6;
  bool reportable = 7;
  repeated string operations = 8;
}

message MetricDef {
  string name = 1;
  string type = 2;
}

message ArtifactDef {
  string name = 1;
  string content_type = 2;
}

message ValidateRequest {
  string unit_name = 1;
  string kind = 2;
  bytes spec_json = 3;
}

message ValidateResponse {}

message PlanRequest {
  string unit_name = 1;
  string kind = 2;
  string run_id = 3;
  int64 run_duration_millis = 4;
  int32 worker_count = 5;
  bytes spec_json = 6;
}

message PlanResponse {
  bytes plan_json = 1;
}

message RunRequest {
  string unit_name = 1;
  string kind = 2;
  string run_id = 3;
  int64 run_duration_millis = 4;
  int32 worker_count = 5;
  bytes spec_json = 6;
  map<string, PortValue> inputs = 7;
}

message RunEnvRequest {
  string op = 1;
  string name = 2;
  PortValue value = 3;
}

message RunEnvResponse {
  string op = 1;
  string name = 2;
  PortValue value = 3;
}

message PortValue {
  string type = 1;
  string encoding = 2;
  string transport = 3;
  bool sensitive = 4;
  bool reportable = 5;
  bytes payload = 6;
}

message MetricFlush {
  repeated MetricSnapshot metrics = 1;
}

message MetricSnapshot {
  string name = 1;
  string type = 2;
  map<string, string> labels = 3;
  int64 count = 4;
  double sum = 5;
  double min = 6;
  double max = 7;
}

message SetOutput {
  string name = 1;
  PortValue value = 2;
}

message TerminalStatus {
  bool ok = 1;
  Error error = 2;
}

message Error {
  string code = 1;
  string message = 2;
}
```

- [ ] **Step 2: Generate Go protobuf types**

Run:

```bash
GOWORK=off go get google.golang.org/protobuf@v1.36.6
GOWORK=off go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
protoc --go_out=. --go_opt=module=github.com/WuKongIM/wkbench benchkit/protocol/wkbench_plugin.proto
```

Expected: `benchkit/protocol/wkbench_plugin.pb.go` is created and `go.mod` includes `google.golang.org/protobuf`.

- [ ] **Step 3: Verify generated code compiles**

Run:

```bash
GOWORK=off go test ./benchkit/protocol
```

Expected: command exits 0.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum benchkit/protocol/wkbench_plugin.proto benchkit/protocol/wkbench_plugin.pb.go
git commit -m "feat: add plugin protocol schema"
```

## Task 3: Add Frame Codec

**Files:**
- Create: `benchkit/protocol/frame.go`
- Create: `benchkit/protocol/frame_test.go`

- [ ] **Step 1: Write frame round-trip tests**

Create `benchkit/protocol/frame_test.go`:

```go
package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameCodecRoundTripsMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)
	first := &Frame{RequestId: "one", Body: &Frame_HandshakeRequest{HandshakeRequest: &HandshakeRequest{HostProtocol: "wkbench.plugin/v1"}}}
	second := &Frame{RequestId: "two", Body: &Frame_ListUnitsRequest{ListUnitsRequest: &ListUnitsRequest{}}}
	if err := writer.WriteFrame(first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := writer.WriteFrame(second); err != nil {
		t.Fatalf("write second: %v", err)
	}

	reader := NewFrameReader(&buf, 1024)
	gotFirst, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	gotSecond, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if gotFirst.RequestId != "one" || gotSecond.RequestId != "two" {
		t.Fatalf("unexpected ids: %q %q", gotFirst.RequestId, gotSecond.RequestId)
	}
}

func TestFrameReaderRejectsOversizedFrame(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)
	if err := writer.WriteFrame(&Frame{RequestId: "too-big", Body: &Frame_Error{Error: &Error{Message: "payload"}}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	reader := NewFrameReader(&buf, 1)
	_, err := reader.ReadFrame()
	if err == nil {
		t.Fatal("expected oversized frame error")
	}
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameReaderReturnsEOF(t *testing.T) {
	reader := NewFrameReader(bytes.NewReader(nil), 1024)
	_, err := reader.ReadFrame()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want EOF", err)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./benchkit/protocol -run Frame -count=1
```

Expected: FAIL because frame codec types do not exist.

- [ ] **Step 3: Implement frame codec**

Create `benchkit/protocol/frame.go`:

```go
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

var ErrFrameTooLarge = errors.New("plugin frame too large")

type FrameWriter struct {
	w io.Writer
}

func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

func (w *FrameWriter) WriteFrame(frame *Frame) error {
	payload, err := proto.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.w.Write(header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := w.w.Write(payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}

type FrameReader struct {
	r        io.Reader
	maxBytes uint32
}

func NewFrameReader(r io.Reader, maxBytes int) *FrameReader {
	return &FrameReader{r: r, maxBytes: uint32(maxBytes)}
}

func (r *FrameReader) ReadFrame() (*Frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > r.maxBytes {
		return nil, ErrFrameTooLarge
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r.r, payload); err != nil {
		return nil, err
	}
	var frame Frame
	if err := proto.Unmarshal(payload, &frame); err != nil {
		return nil, fmt.Errorf("unmarshal frame: %w", err)
	}
	return &frame, nil
}
```

- [ ] **Step 4: Run protocol tests**

Run:

```bash
GOWORK=off go test ./benchkit/protocol
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add benchkit/protocol/frame.go benchkit/protocol/frame_test.go
git commit -m "feat: add plugin frame codec"
```

## Task 4: Add Plugin Catalog Resolution

**Files:**
- Create: `benchkit/pluginhost/manifest.go`
- Create: `benchkit/pluginhost/catalog.go`
- Create: `benchkit/pluginhost/catalog_test.go`

- [ ] **Step 1: Write catalog tests**

Create `benchkit/pluginhost/catalog_test.go`:

```go
package pluginhost

import (
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestCatalogResolvesUniqueKind(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "acme.system", Version: "0.1.0", Units: []Unit{{Kind: "acme.echo/v1"}}},
	})
	unit, err := catalog.Resolve("acme.echo/v1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if unit.PluginName != "acme.system" || unit.Kind != "acme.echo/v1" {
		t.Fatalf("unexpected unit: %#v", unit)
	}
}

func TestCatalogRejectsAmbiguousUnqualifiedKind(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "first", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
		{Name: "second", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
	})
	_, err := catalog.Resolve("demo.echo/v1")
	if err == nil {
		t.Fatal("expected ambiguity")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q, want ambiguous", err.Error())
	}
}

func TestCatalogResolvesExplicitPluginKind(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{Name: "first", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
		{Name: "second", Version: "0.1.0", Units: []Unit{{Kind: "demo.echo/v1"}}},
	})
	unit, err := catalog.Resolve("second:demo.echo/v1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if unit.PluginName != "second" {
		t.Fatalf("PluginName = %q", unit.PluginName)
	}
}

func TestCatalogPreservesPortMetadata(t *testing.T) {
	catalog := NewCatalog([]Plugin{
		{
			Name: "acme.system",
			Units: []Unit{{
				Kind: "acme.echo/v1",
				Outputs: []contract.PortDef{{
					Name: "result",
					Type: "port.demo.echo/v1",
					Meta: contract.PortMeta{Reportable: true},
				}},
			}},
		},
	})
	unit, err := catalog.Resolve("acme.echo/v1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !unit.Outputs[0].Metadata().Reportable {
		t.Fatal("reportable metadata was not preserved")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run Catalog -count=1
```

Expected: FAIL because `benchkit/pluginhost` does not exist.

- [ ] **Step 3: Add manifest types**

Create `benchkit/pluginhost/manifest.go`:

```go
package pluginhost

import "github.com/WuKongIM/wkbench/benchkit/contract"

type Plugin struct {
	Name     string
	Version  string
	Protocol string
	Source   string
	Checksum string
	Units    []Unit
}

type Unit struct {
	PluginName string
	Kind       string
	Title      string
	Description string
	Inputs     []contract.PortDef
	Outputs    []contract.PortDef
	Metrics    []contract.MetricDef
	Artifacts  []contract.ArtifactDef
}

func (u Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        u.Kind,
		Title:       u.Title,
		Description: u.Description,
		Inputs:      u.Inputs,
		Outputs:     u.Outputs,
		Metrics:     u.Metrics,
		Artifacts:   u.Artifacts,
	}
}
```

- [ ] **Step 4: Add catalog implementation**

Create `benchkit/pluginhost/catalog.go`:

```go
package pluginhost

import (
	"fmt"
	"sort"
	"strings"
)

type Catalog struct {
	unitsByKind map[string][]Unit
}

func NewCatalog(plugins []Plugin) *Catalog {
	unitsByKind := make(map[string][]Unit)
	for _, plugin := range plugins {
		for _, unit := range plugin.Units {
			unit.PluginName = plugin.Name
			unitsByKind[unit.Kind] = append(unitsByKind[unit.Kind], unit)
		}
	}
	for kind := range unitsByKind {
		sort.Slice(unitsByKind[kind], func(i, j int) bool {
			return unitsByKind[kind][i].PluginName < unitsByKind[kind][j].PluginName
		})
	}
	return &Catalog{unitsByKind: unitsByKind}
}

func (c *Catalog) Resolve(use string) (Unit, error) {
	use = strings.TrimSpace(use)
	if use == "" {
		return Unit{}, fmt.Errorf("unit kind is required")
	}
	if pluginName, kind, ok := strings.Cut(use, ":"); ok {
		for _, unit := range c.unitsByKind[kind] {
			if unit.PluginName == pluginName {
				return unit, nil
			}
		}
		return Unit{}, fmt.Errorf("unit kind %q from plugin %q is not registered", kind, pluginName)
	}
	matches := c.unitsByKind[use]
	switch len(matches) {
	case 0:
		return Unit{}, fmt.Errorf("unit kind %q is not registered", use)
	case 1:
		return matches[0], nil
	default:
		plugins := make([]string, 0, len(matches))
		for _, unit := range matches {
			plugins = append(plugins, unit.PluginName)
		}
		return Unit{}, fmt.Errorf("unit kind %q is ambiguous across plugins: %s", use, strings.Join(plugins, ", "))
	}
}
```

- [ ] **Step 5: Run catalog tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run Catalog -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add benchkit/pluginhost/manifest.go benchkit/pluginhost/catalog.go benchkit/pluginhost/catalog_test.go
git commit -m "feat: add plugin catalog resolution"
```

## Task 5: Add Remote Unit Proxy

**Files:**
- Create: `benchkit/pluginhost/client.go`
- Create: `benchkit/pluginhost/remote_unit.go`
- Create: `benchkit/pluginhost/remote_unit_test.go`

- [ ] **Step 1: Write remote unit tests**

Create `benchkit/pluginhost/remote_unit_test.go`:

```go
package pluginhost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestRemoteUnitDelegatesValidatePlanAndRun(t *testing.T) {
	client := &fakeClient{}
	unit := NewRemoteUnit(client, Unit{PluginName: "demo", Kind: "demo.echo/v1", Title: "Echo"})
	env := contract.NewTestRunEnv("run-1", "echo", map[string]any{"message": "hi"}, nil)

	if err := unit.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
	plan, err := unit.Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.UnitName != "echo" {
		t.Fatalf("UnitName = %q", plan.UnitName)
	}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := contract.Output[string](env, "result")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if got != "hi" {
		t.Fatalf("result = %q", got)
	}
	if !client.validateCalled || !client.planCalled || !client.runCalled {
		t.Fatalf("client calls missing: %#v", client)
	}
}

type fakeClient struct {
	validateCalled bool
	planCalled     bool
	runCalled      bool
}

func (f *fakeClient) Validate(ctx context.Context, req UnitRequest) error {
	f.validateCalled = true
	return nil
}

func (f *fakeClient) Plan(ctx context.Context, req UnitRequest) (contract.Plan, error) {
	f.planCalled = true
	return contract.Plan{UnitName: req.UnitName}, nil
}

func (f *fakeClient) Run(ctx context.Context, req RunRequest, env contract.RunEnv) error {
	f.runCalled = true
	var spec struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(req.SpecJSON, &spec); err != nil {
		return err
	}
	return env.SetOutput("result", spec.Message)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run RemoteUnit -count=1
```

Expected: FAIL because proxy types do not exist.

- [ ] **Step 3: Add client interface**

Create `benchkit/pluginhost/client.go`:

```go
package pluginhost

import (
	"context"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

type Client interface {
	Validate(context.Context, UnitRequest) error
	Plan(context.Context, UnitRequest) (contract.Plan, error)
	Run(context.Context, RunRequest, contract.RunEnv) error
}

type UnitRequest struct {
	PluginName        string
	UnitName          string
	Kind              string
	RunID             string
	RunDurationMillis int64
	WorkerCount       int
	SpecJSON          []byte
}

type RunRequest struct {
	UnitRequest
	Inputs map[string]any
}
```

- [ ] **Step 4: Add remote unit implementation**

Create `benchkit/pluginhost/remote_unit.go`:

```go
package pluginhost

import (
	"context"
	"encoding/json"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

type RemoteUnit struct {
	client Client
	unit   Unit
}

func NewRemoteUnit(client Client, unit Unit) RemoteUnit {
	return RemoteUnit{client: client, unit: unit}
}

func (u RemoteUnit) Definition() contract.Definition {
	return u.unit.Definition()
}

func (u RemoteUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	spec, err := encodeSpec(env)
	if err != nil {
		return err
	}
	return u.client.Validate(ctx, UnitRequest{
		PluginName: u.unit.PluginName,
		UnitName:   env.UnitName(),
		Kind:       u.unit.Kind,
		SpecJSON:   spec,
	})
}

func (u RemoteUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	spec, err := encodeSpec(env)
	if err != nil {
		return contract.Plan{}, err
	}
	return u.client.Plan(ctx, UnitRequest{
		PluginName: u.unit.PluginName,
		UnitName:   env.UnitName(),
		Kind:       u.unit.Kind,
		RunID:      env.RunID(),
		RunDurationMillis: env.RunDuration().Milliseconds(),
		WorkerCount:       env.WorkerCount(),
		SpecJSON:   spec,
	})
}

func (u RemoteUnit) Run(ctx context.Context, env contract.RunEnv) error {
	spec, err := encodeSpec(env)
	if err != nil {
		return err
	}
	return u.client.Run(ctx, RunRequest{
		UnitRequest: UnitRequest{
			PluginName: u.unit.PluginName,
			UnitName:   env.UnitName(),
			Kind:       u.unit.Kind,
			RunID:      env.RunID(),
			RunDurationMillis: env.RunDuration().Milliseconds(),
			WorkerCount:       env.WorkerCount(),
			SpecJSON:   spec,
		},
	}, env)
}

func encodeSpec(env contract.ValidateEnv) ([]byte, error) {
	var spec map[string]any
	if err := env.DecodeSpec(&spec); err != nil {
		return nil, err
	}
	return json.Marshal(spec)
}
```

- [ ] **Step 5: Run pluginhost tests**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run 'Catalog|RemoteUnit' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add benchkit/pluginhost/client.go benchkit/pluginhost/remote_unit.go benchkit/pluginhost/remote_unit_test.go
git commit -m "feat: add remote plugin unit proxy"
```

## Task 6: Add Go Plugin SDK Server

**Files:**
- Create: `sdk/go/wkbench/plugin/server.go`
- Create: `sdk/go/wkbench/plugin/server_test.go`

- [ ] **Step 1: Write SDK definition conversion test**

Create `sdk/go/wkbench/plugin/server_test.go`:

```go
package plugin

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestManifestFromUnitsIncludesPortMetadata(t *testing.T) {
	manifest := ManifestFromUnits("demo.plugin", "0.1.0", []contract.Unit{echoUnit{}})
	if manifest.Name != "demo.plugin" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	if len(manifest.Units) != 1 {
		t.Fatalf("units = %d", len(manifest.Units))
	}
	output := manifest.Units[0].Outputs[0]
	if !output.Meta.Reportable {
		t.Fatalf("output metadata = %#v", output.Meta)
	}
}

type echoUnit struct{}

func (echoUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.echo/v1",
		Outputs: []contract.PortDef{{
			Name: "result",
			Type: "port.demo.echo/v1",
			Meta: contract.PortMeta{Reportable: true},
		}},
	}
}
func (echoUnit) Validate(ctx context.Context, env contract.ValidateEnv) error { return nil }
func (echoUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}
func (echoUnit) Run(ctx context.Context, env contract.RunEnv) error { return nil }
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -run ManifestFromUnits -count=1
```

Expected: FAIL because the SDK package does not exist.

- [ ] **Step 3: Add SDK server manifest code**

Create `sdk/go/wkbench/plugin/server.go`:

```go
package plugin

import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
)

type Plugin struct {
	Name    string
	Version string
	Units   []contract.Unit
}

func ManifestFromUnits(name, version string, units []contract.Unit) pluginhost.Plugin {
	out := pluginhost.Plugin{Name: name, Version: version, Protocol: "wkbench.plugin/v1"}
	for _, unit := range units {
		def := unit.Definition()
		out.Units = append(out.Units, pluginhost.Unit{
			Kind:        def.Kind,
			Title:       def.Title,
			Description: def.Description,
			Inputs:      def.Inputs,
			Outputs:     def.Outputs,
			Metrics:     def.Metrics,
			Artifacts:   def.Artifacts,
		})
	}
	return out
}
```

- [ ] **Step 4: Fix imports and run SDK test**

Ensure `sdk/go/wkbench/plugin/server_test.go` imports `context`:

```go
import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)
```

Run:

```bash
GOWORK=off go test ./sdk/go/wkbench/plugin -run ManifestFromUnits -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sdk/go/wkbench/plugin/server.go sdk/go/wkbench/plugin/server_test.go
git commit -m "feat: add go plugin sdk manifest support"
```

## Task 7: Add Demo Plugin Unit

**Files:**
- Create: `plugins/demo/echo/unit.go`
- Create: `plugins/demo/echo/unit_test.go`
- Create: `plugins/demo/cmd/wkbench-demo-plugin/main.go`

- [ ] **Step 1: Write demo unit tests**

Create `plugins/demo/echo/unit_test.go`:

```go
package echo

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, Unit{})
}

func TestRunPublishesReportableResult(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "echo", map[string]any{"message": "hello"}, nil)
	if err := (Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := contract.Output[Result](env, "result")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if got.Message != "hello" {
		t.Fatalf("Message = %q", got.Message)
	}
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
GOWORK=off go test ./plugins/demo/echo -count=1
```

Expected: FAIL because the package has no implementation.

- [ ] **Step 3: Add demo unit implementation**

Create `plugins/demo/echo/unit.go`:

```go
package echo

import (
	"context"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

const kind = "demo.echo/v1"

type Unit struct{}

type Spec struct {
	Message string `json:"message"`
}

type Result struct {
	Message string `json:"message"`
}

func (r Result) ReportOutput() any {
	return r
}

func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Demo echo",
		Description: "Echoes a message through an external plugin.",
		Outputs: []contract.PortDef{{
			Name: "result",
			Type: "port.demo.echo/v1",
			Meta: contract.PortMeta{
				Boundary:   contract.PortBoundaryData,
				Transport:  contract.PortTransportInline,
				Reportable: true,
			},
		}},
	}
}

func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	var spec Spec
	return env.DecodeSpec(&spec)
}

func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return err
	}
	return env.SetOutput("result", Result{Message: spec.Message})
}
```

- [ ] **Step 4: Add demo plugin main**

Create `plugins/demo/cmd/wkbench-demo-plugin/main.go`:

```go
package main

import (
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/plugins/demo/echo"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name:    "wkbench.demo",
		Version: "0.1.0",
		Units:   []contract.Unit{echo.Unit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run demo unit tests**

Run:

```bash
GOWORK=off go test ./plugins/demo/echo
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add plugins/demo/echo/unit.go plugins/demo/echo/unit_test.go plugins/demo/cmd/wkbench-demo-plugin/main.go
git commit -m "feat: add demo plugin unit"
```

## Task 8: Implement SDK Serve and Stdio Client

**Files:**
- Modify: `sdk/go/wkbench/plugin/server.go`
- Create: `benchkit/pluginhost/stdio_client.go`
- Create: `benchkit/pluginhost/stdio_client_test.go`

- [ ] **Step 1: Write stdio client smoke test**

Create `benchkit/pluginhost/stdio_client_test.go`:

```go
package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStdioClientListsDemoPluginUnits(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "wkbench-demo-plugin")
	build := exec.Command("go", "build", "-o", bin, "./plugins/demo/cmd/wkbench-demo-plugin")
	build.Env = append(os.Environ(), "GOWORK=off")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

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
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run StdioClient -count=1
```

Expected: FAIL because `Serve`, `StartStdioClient`, and plugin process protocol are incomplete.

- [ ] **Step 3: Add SDK Serve handshake support**

In `sdk/go/wkbench/plugin/server.go`, add:

```go
import (
	"fmt"
	"io"

	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

func Serve(plugin Plugin, stdin io.Reader, stdout io.Writer) error {
	manifest := ManifestFromUnits(plugin.Name, plugin.Version, plugin.Units)
	reader := protocol.NewFrameReader(stdin, 16<<20)
	writer := protocol.NewFrameWriter(stdout)
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read plugin frame: %w", err)
		}
		switch body := frame.Body.(type) {
		case *protocol.Frame_HandshakeRequest:
			_ = body
			if err := writer.WriteFrame(&protocol.Frame{
				RequestId: frame.RequestId,
				Body: &protocol.Frame_HandshakeResponse{HandshakeResponse: &protocol.HandshakeResponse{
					Manifest:         manifestToProto(manifest),
					SelectedProtocol: "wkbench.plugin/v1",
				}},
			}); err != nil {
				return err
			}
		case *protocol.Frame_ListUnitsRequest:
			_ = body
			if err := writer.WriteFrame(&protocol.Frame{
				RequestId: frame.RequestId,
				Body: &protocol.Frame_ListUnitsResponse{ListUnitsResponse: &protocol.ListUnitsResponse{
					Units: manifestToProto(manifest).Units,
				}},
			}); err != nil {
				return err
			}
		default:
			if err := writer.WriteFrame(&protocol.Frame{
				RequestId: frame.RequestId,
				Body: &protocol.Frame_Error{Error: &protocol.Error{
					Code:    "UNSUPPORTED",
					Message: "unsupported frame",
				}},
			}); err != nil {
				return err
			}
		}
	}
}
```

Append these conversion functions in the same file:

```go
func manifestToProto(manifest pluginhost.Plugin) *protocol.PluginManifest {
	out := &protocol.PluginManifest{
		Name:     manifest.Name,
		Version:  manifest.Version,
		Protocol: manifest.Protocol,
		Source:   manifest.Source,
		Checksum: manifest.Checksum,
	}
	for _, unit := range manifest.Units {
		out.Units = append(out.Units, unitToProto(unit))
	}
	return out
}

func unitToProto(unit pluginhost.Unit) *protocol.UnitDefinition {
	out := &protocol.UnitDefinition{
		Kind:        unit.Kind,
		Title:       unit.Title,
		Description: unit.Description,
	}
	for _, port := range unit.Inputs {
		out.Inputs = append(out.Inputs, portToProto(port))
	}
	for _, port := range unit.Outputs {
		out.Outputs = append(out.Outputs, portToProto(port))
	}
	for _, metric := range unit.Metrics {
		out.Metrics = append(out.Metrics, &protocol.MetricDef{Name: metric.Name, Type: metric.Type})
	}
	for _, artifact := range unit.Artifacts {
		out.Artifacts = append(out.Artifacts, &protocol.ArtifactDef{Name: artifact.Name, ContentType: artifact.ContentType})
	}
	return out
}

func portToProto(port contract.PortDef) *protocol.PortDef {
	meta := port.Metadata()
	return &protocol.PortDef{
		Name:     port.Name,
		Type:     string(port.Type),
		Optional: port.Optional,
		Meta: &protocol.PortMeta{
			Boundary:        string(meta.Boundary),
			Transport:       string(meta.Transport),
			Schema:          meta.Schema,
			Encodings:       meta.Encodings,
			MaxPayloadBytes: meta.MaxPayloadBytes,
			Sensitive:       meta.Sensitive,
			Reportable:      meta.Reportable,
			Operations:      meta.Operations,
		},
	}
}
```

- [ ] **Step 4: Add stdio client handshake implementation**

Create `benchkit/pluginhost/stdio_client.go`:

```go
package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

type StdioClient struct {
	cmd     *exec.Cmd
	reader  *protocol.FrameReader
	writer  *protocol.FrameWriter
	nextSeq int64
}

func StartStdioClient(ctx context.Context, path string) (*StdioClient, error) {
	cmd := exec.CommandContext(ctx, path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin: %w", err)
	}
	return &StdioClient{
		cmd:    cmd,
		reader: protocol.NewFrameReader(stdout, 16<<20),
		writer: protocol.NewFrameWriter(stdin),
	}, nil
}

func (c *StdioClient) Close() error {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

func (c *StdioClient) Handshake(ctx context.Context) (Plugin, error) {
	if err := c.writer.WriteFrame(&protocol.Frame{
		RequestId: "handshake-1",
		Body: &protocol.Frame_HandshakeRequest{HandshakeRequest: &protocol.HandshakeRequest{
			HostProtocol: "wkbench.plugin/v1",
			MinProtocol:  "wkbench.plugin/v1",
			MaxProtocol:  "wkbench.plugin/v1",
		}},
	}); err != nil {
		return Plugin{}, err
	}
	frame, err := c.reader.ReadFrame()
	if err != nil {
		return Plugin{}, err
	}
	body, ok := frame.Body.(*protocol.Frame_HandshakeResponse)
	if !ok {
		return Plugin{}, fmt.Errorf("unexpected handshake response %T", frame.Body)
	}
	return pluginFromProto(body.HandshakeResponse.Manifest), nil
}
```

Append these conversion functions in the same file:

```go
func pluginFromProto(manifest *protocol.PluginManifest) Plugin {
	out := Plugin{
		Name:     manifest.Name,
		Version:  manifest.Version,
		Protocol: manifest.Protocol,
		Source:   manifest.Source,
		Checksum: manifest.Checksum,
	}
	for _, unit := range manifest.Units {
		out.Units = append(out.Units, unitFromProto(unit))
	}
	return out
}

func unitFromProto(unit *protocol.UnitDefinition) Unit {
	out := Unit{
		Kind:        unit.Kind,
		Title:       unit.Title,
		Description: unit.Description,
	}
	for _, port := range unit.Inputs {
		out.Inputs = append(out.Inputs, portFromProto(port))
	}
	for _, port := range unit.Outputs {
		out.Outputs = append(out.Outputs, portFromProto(port))
	}
	for _, metric := range unit.Metrics {
		out.Metrics = append(out.Metrics, contract.MetricDef{Name: metric.Name, Type: metric.Type})
	}
	for _, artifact := range unit.Artifacts {
		out.Artifacts = append(out.Artifacts, contract.ArtifactDef{Name: artifact.Name, ContentType: artifact.ContentType})
	}
	return out
}

func portFromProto(port *protocol.PortDef) contract.PortDef {
	out := contract.PortDef{
		Name:     port.Name,
		Type:     contract.PortType(port.Type),
		Optional: port.Optional,
	}
	if port.Meta != nil {
		out.Meta = contract.PortMeta{
			Boundary:        contract.PortBoundary(port.Meta.Boundary),
			Transport:       contract.PortTransport(port.Meta.Transport),
			Schema:          port.Meta.Schema,
			Encodings:       append([]string(nil), port.Meta.Encodings...),
			MaxPayloadBytes: port.Meta.MaxPayloadBytes,
			Sensitive:       port.Meta.Sensitive,
			Reportable:      port.Meta.Reportable,
			Operations:      append([]string(nil), port.Meta.Operations...),
		}
	}
	return out
}
```

- [ ] **Step 5: Run stdio client test**

Run:

```bash
GOWORK=off go test ./benchkit/pluginhost -run StdioClient -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sdk/go/wkbench/plugin/server.go benchkit/pluginhost/stdio_client.go benchkit/pluginhost/stdio_client_test.go
git commit -m "feat: add stdio plugin handshake"
```

## Task 9: Wire Plugin Loading Into CLI

**Files:**
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`
- Create: `examples/plugin-echo.yaml`

- [ ] **Step 1: Add CLI test for plugin list-units**

In `cmd/wkbench/main_test.go`, add:

```go
func TestListUnitsIncludesExternalPlugin(t *testing.T) {
	bin := buildDemoPlugin(t)
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "list-units"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "demo.echo/v1") {
		t.Fatalf("list-units missing plugin unit:\n%s", stderr.String())
	}
}
```

Add this helper function:

```go
func buildDemoPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "wkbench-demo-plugin")
	cmd := exec.Command("go", "build", "-o", bin, "./plugins/demo/cmd/wkbench-demo-plugin")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build demo plugin: %v\n%s", err, out)
	}
	return bin
}
```

Ensure the import block includes these packages:

```go
import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run CLI test and verify failure**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run TestListUnitsIncludesExternalPlugin -count=1
```

Expected: FAIL because `-plugin` is not parsed.

- [ ] **Step 3: Parse global plugin flags**

In `cmd/wkbench/main.go`, add:

```go
type cliConfig struct {
	Plugins []string
	Command string
	Args    []string
}

func parseGlobalArgs(args []string, stderr io.Writer) (cliConfig, int) {
	fs := flag.NewFlagSet("wkbench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var plugins multiString
	fs.Var(&plugins, "plugin", "external wkbench plugin executable; may be repeated")
	if err := fs.Parse(args); err != nil {
		return cliConfig{}, exitConfig
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench [-plugin path] <list-units|new-unit|explain|plan|validate|run>")
		return cliConfig{}, exitConfig
	}
	return cliConfig{Plugins: plugins, Command: rest[0], Args: rest[1:]}, exitOK
}

type multiString []string

func (m *multiString) String() string {
	return strings.Join(*m, ",")
}

func (m *multiString) Set(value string) error {
	*m = append(*m, value)
	return nil
}
```

Update `runWithStderr`:

```go
func runWithStderr(args []string, stderr io.Writer) int {
	cfg, code := parseGlobalArgs(args, stderr)
	if code != exitOK {
		return code
	}
	reg := defaultRegistry()
	if code := loadExternalPlugins(reg, cfg.Plugins, stderr); code != exitOK {
		return code
	}
	switch cfg.Command {
	case "list-units":
		return runListUnits(reg, stderr)
	case "new-unit":
		return runNewUnit(cfg.Args, stderr)
	case "explain":
		return runExplain(reg, cfg.Args, stderr)
	case "plan":
		return runPlan(reg, cfg.Args, stderr)
	case "validate":
		return runValidate(reg, cfg.Args, stderr)
	case "run":
		return runScenario(reg, cfg.Args, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", cfg.Command)
		return exitConfig
	}
}
```

Add `strings` and `pluginhost` imports.

- [ ] **Step 4: Add external plugin registration**

In `cmd/wkbench/main.go`, add:

```go
func loadExternalPlugins(reg *registry.Registry, paths []string, stderr io.Writer) int {
	for _, path := range paths {
		client, err := pluginhost.StartStdioClient(context.Background(), path)
		if err != nil {
			fmt.Fprintf(stderr, "plugin %s failed to start: %v\n", path, err)
			return exitConfig
		}
		manifest, err := client.Handshake(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "plugin %s handshake failed: %v\n", path, err)
			return exitConfig
		}
		for _, unit := range manifest.Units {
			if err := reg.Register(pluginhost.NewRemoteUnit(client, unit)); err != nil {
				fmt.Fprintf(stderr, "plugin %s registration failed: %v\n", path, err)
				return exitConfig
			}
		}
	}
	return exitOK
}
```

- [ ] **Step 5: Add plugin echo scenario**

Create `examples/plugin-echo.yaml`:

```yaml
version: wkbench/v2

run:
  id: plugin-echo-demo
  duration: 1s

units:
  echo:
    use: demo.echo/v1
    spec:
      message: hello from plugin
```

- [ ] **Step 6: Run CLI plugin list test**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run TestListUnitsIncludesExternalPlugin -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/wkbench/main.go cmd/wkbench/main_test.go examples/plugin-echo.yaml
git commit -m "feat: load external plugin units in cli"
```

## Task 10: Implement Remote Validate Plan Run

**Files:**
- Modify: `sdk/go/wkbench/plugin/server.go`
- Modify: `benchkit/pluginhost/stdio_client.go`
- Modify: `benchkit/pluginhost/stdio_client_test.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Add CLI validate/run tests**

In `cmd/wkbench/main_test.go`, add:

```go
func TestValidateExternalPluginScenario(t *testing.T) {
	bin := buildDemoPlugin(t)
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "validate", "-scenario", "./examples/plugin-echo.yaml"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench scenario is valid") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
}

func TestRunExternalPluginScenario(t *testing.T) {
	bin := buildDemoPlugin(t)
	var stderr bytes.Buffer
	code := runWithStderr([]string{"-plugin", bin, "run", "-scenario", "./examples/plugin-echo.yaml"}, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench run completed") {
		t.Fatalf("unexpected stderr:\n%s", stderr.String())
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'ExternalPluginScenario|ExternalPlugin' -count=1
```

Expected: FAIL because remote validate/run support is not implemented.

- [ ] **Step 3: Implement SDK unit lookup and lifecycle frame handlers**

In `sdk/go/wkbench/plugin/server.go`, add this internal server type:

Ensure the import block includes:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)
```

```go
type server struct {
	manifest    pluginhost.Plugin
	unitsByKind map[string]contract.Unit
}

func newServer(plugin Plugin) *server {
	unitsByKind := make(map[string]contract.Unit, len(plugin.Units))
	for _, unit := range plugin.Units {
		unitsByKind[unit.Definition().Kind] = unit
	}
	return &server{
		manifest:    ManifestFromUnits(plugin.Name, plugin.Version, plugin.Units),
		unitsByKind: unitsByKind,
	}
}

func (s *server) unit(kind string) (contract.Unit, error) {
	unit, ok := s.unitsByKind[kind]
	if !ok {
		return nil, fmt.Errorf("unit kind %q is not registered", kind)
	}
	return unit, nil
}
```

Replace the local manifest variable in `Serve` with:

```go
srv := newServer(plugin)
```

Then use `srv.manifest` in handshake and list-units responses.

Add these helpers:

```go
func decodeSpecMap(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("decode spec json: %w", err)
	}
	return spec, nil
}

func encodeJSONPayload(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode json payload: %w", err)
	}
	return payload, nil
}
```

Add this validate handler:

```go
func (s *server) handleValidate(ctx context.Context, frame *protocol.Frame, req *protocol.ValidateRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.Kind)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "CONFIG_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.SpecJson)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "CONFIG_ERROR", err.Error())
	}
	env := contract.NewTestRunEnv("", req.UnitName, nil, spec)
	if err := unit.Validate(ctx, env); err != nil {
		return writeProtocolError(writer, frame.RequestId, "CONFIG_ERROR", err.Error())
	}
	return writer.WriteFrame(&protocol.Frame{
		RequestId: frame.RequestId,
		Body:      &protocol.Frame_ValidateResponse{ValidateResponse: &protocol.ValidateResponse{}},
	})
}
```

Add this plan handler:

```go
func (s *server) handlePlan(ctx context.Context, frame *protocol.Frame, req *protocol.PlanRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.Kind)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "PLAN_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.SpecJson)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "PLAN_ERROR", err.Error())
	}
	env := contract.NewTestRunEnv(req.RunId, req.UnitName, nil, spec)
	env.SetRunDuration(time.Duration(req.RunDurationMillis) * time.Millisecond)
	plan, err := unit.Plan(ctx, env)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "PLAN_ERROR", err.Error())
	}
	payload, err := encodeJSONPayload(plan)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "PLAN_ERROR", err.Error())
	}
	return writer.WriteFrame(&protocol.Frame{
		RequestId: frame.RequestId,
		Body:      &protocol.Frame_PlanResponse{PlanResponse: &protocol.PlanResponse{PlanJson: payload}},
	})
}
```

Add this run handler:

```go
func (s *server) handleRun(ctx context.Context, frame *protocol.Frame, req *protocol.RunRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.Kind)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "RUN_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.SpecJson)
	if err != nil {
		return writeProtocolError(writer, frame.RequestId, "RUN_ERROR", err.Error())
	}
	inputs := make(map[string]any, len(req.Inputs))
	for name, value := range req.Inputs {
		var decoded any
		if len(value.Payload) > 0 {
			if err := json.Unmarshal(value.Payload, &decoded); err != nil {
				return writeProtocolError(writer, frame.RequestId, "RUN_ERROR", err.Error())
			}
		}
		inputs[name] = decoded
	}
	env := contract.NewTestRunEnv(req.RunId, req.UnitName, inputs, spec)
	env.SetRunDuration(time.Duration(req.RunDurationMillis) * time.Millisecond)
	if err := unit.Run(ctx, env); err != nil {
		return writeProtocolError(writer, frame.RequestId, "RUN_ERROR", err.Error())
	}
	for _, output := range unit.Definition().Outputs {
		value, ok := env.Output(output.Name)
		if !ok {
			continue
		}
		payload, err := encodeJSONPayload(value)
		if err != nil {
			return writeProtocolError(writer, frame.RequestId, "RUN_ERROR", err.Error())
		}
		meta := output.Metadata()
		if err := writer.WriteFrame(&protocol.Frame{
			RequestId: frame.RequestId,
			Body: &protocol.Frame_SetOutput{SetOutput: &protocol.SetOutput{
				Name: output.Name,
				Value: &protocol.PortValue{
					Type:       string(output.Type),
					Encoding:   "json",
					Transport:  string(meta.Transport),
					Sensitive:  meta.Sensitive,
					Reportable: meta.Reportable,
					Payload:    payload,
				},
			}},
		}); err != nil {
			return err
		}
	}
	return writer.WriteFrame(&protocol.Frame{
		RequestId: frame.RequestId,
		Body:      &protocol.Frame_TerminalStatus{TerminalStatus: &protocol.TerminalStatus{Ok: true}},
	})
}
```

Add this shared error writer:

```go
func writeProtocolError(writer *protocol.FrameWriter, requestID, code, message string) error {
	return writer.WriteFrame(&protocol.Frame{
		RequestId: requestID,
		Body: &protocol.Frame_Error{Error: &protocol.Error{
			Code:    code,
			Message: message,
		}},
	})
}
```

Extend the `Serve` switch to call these handlers for `ValidateRequest`, `PlanRequest`, and `RunRequest`.

- [ ] **Step 4: Implement stdio client lifecycle calls**

In `benchkit/pluginhost/stdio_client.go`, append:

```go
func (c *StdioClient) nextRequestID(prefix string) string {
	c.nextSeq++
	return fmt.Sprintf("%s-%d", prefix, c.nextSeq)
}

func (c *StdioClient) request(frame *protocol.Frame) (*protocol.Frame, error) {
	if err := c.writer.WriteFrame(frame); err != nil {
		return nil, err
	}
	response, err := c.reader.ReadFrame()
	if err != nil {
		return nil, err
	}
	if response.RequestId != frame.RequestId {
		return nil, fmt.Errorf("plugin response id %q does not match request id %q", response.RequestId, frame.RequestId)
	}
	if body, ok := response.Body.(*protocol.Frame_Error); ok {
		return nil, fmt.Errorf("%s: %s", body.Error.Code, body.Error.Message)
	}
	return response, nil
}

func (c *StdioClient) Validate(ctx context.Context, req UnitRequest) error {
	_, err := c.request(&protocol.Frame{
		RequestId:      c.nextRequestID("validate"),
		RunId:          req.RunID,
		UnitInstanceId: req.UnitName,
		Body: &protocol.Frame_ValidateRequest{ValidateRequest: &protocol.ValidateRequest{
			UnitName: req.UnitName,
			Kind:     req.Kind,
			SpecJson: req.SpecJSON,
		}},
	})
	return err
}

func (c *StdioClient) Plan(ctx context.Context, req UnitRequest) (contract.Plan, error) {
	response, err := c.request(&protocol.Frame{
		RequestId:      c.nextRequestID("plan"),
		RunId:          req.RunID,
		UnitInstanceId: req.UnitName,
		Body: &protocol.Frame_PlanRequest{PlanRequest: &protocol.PlanRequest{
			UnitName:          req.UnitName,
			Kind:              req.Kind,
			RunId:             req.RunID,
			RunDurationMillis: req.RunDurationMillis,
			WorkerCount:       int32(req.WorkerCount),
			SpecJson:          req.SpecJSON,
		}},
	})
	if err != nil {
		return contract.Plan{}, err
	}
	body, ok := response.Body.(*protocol.Frame_PlanResponse)
	if !ok {
		return contract.Plan{}, fmt.Errorf("unexpected plan response %T", response.Body)
	}
	var plan contract.Plan
	if err := json.Unmarshal(body.PlanResponse.PlanJson, &plan); err != nil {
		return contract.Plan{}, fmt.Errorf("decode plan json: %w", err)
	}
	return plan, nil
}

func (c *StdioClient) Run(ctx context.Context, req RunRequest, env contract.RunEnv) error {
	requestID := c.nextRequestID("run")
	if err := c.writer.WriteFrame(&protocol.Frame{
		RequestId:      requestID,
		RunId:          req.RunID,
		UnitInstanceId: req.UnitName,
		Body: &protocol.Frame_RunRequest{RunRequest: &protocol.RunRequest{
			UnitName:          req.UnitName,
			Kind:              req.Kind,
			RunId:             req.RunID,
			RunDurationMillis: req.RunDurationMillis,
			WorkerCount:       int32(req.WorkerCount),
			SpecJson:          req.SpecJSON,
		}},
	}); err != nil {
		return err
	}
	for {
		response, err := c.reader.ReadFrame()
		if err != nil {
			return err
		}
		if response.RequestId != requestID {
			return fmt.Errorf("plugin response id %q does not match request id %q", response.RequestId, requestID)
		}
		switch body := response.Body.(type) {
		case *protocol.Frame_SetOutput:
			var decoded any
			if len(body.SetOutput.Value.Payload) > 0 {
				if err := json.Unmarshal(body.SetOutput.Value.Payload, &decoded); err != nil {
					return fmt.Errorf("decode output %q: %w", body.SetOutput.Name, err)
				}
			}
			if err := env.SetOutput(body.SetOutput.Name, decoded); err != nil {
				return err
			}
		case *protocol.Frame_TerminalStatus:
			if body.TerminalStatus.Ok {
				return nil
			}
			if body.TerminalStatus.Error != nil {
				return fmt.Errorf("%s: %s", body.TerminalStatus.Error.Code, body.TerminalStatus.Error.Message)
			}
			return fmt.Errorf("plugin run failed")
		case *protocol.Frame_Error:
			return fmt.Errorf("%s: %s", body.Error.Code, body.Error.Message)
		default:
			return fmt.Errorf("unexpected run response %T", response.Body)
		}
	}
}
```

- [ ] **Step 5: Run remote scenario tests**

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'ExternalPluginScenario|ExternalPlugin' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sdk/go/wkbench/plugin/server.go benchkit/pluginhost/stdio_client.go benchkit/pluginhost/stdio_client_test.go cmd/wkbench/main_test.go
git commit -m "feat: run external plugin units"
```

## Task 11: Document Phase 1 Plugin Usage

**Files:**
- Modify: `README.md`
- Create: `docs/plugin-authoring.md`

- [ ] **Step 1: Add README plugin commands**

Add this section to `README.md`:

```markdown
## External Plugins

Phase 1 supports loading external plugin executables with the global `-plugin`
flag:

```bash
GOWORK=off go build -o /tmp/wkbench-demo-plugin ./plugins/demo/cmd/wkbench-demo-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin list-units
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin validate -scenario ./examples/plugin-echo.yaml
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin run -scenario ./examples/plugin-echo.yaml
```

External plugin units are temporary registered as remote unit proxies while
official units migrate to plugins. The final architecture removes direct unit
registration from the host binary.
```

- [ ] **Step 2: Add plugin authoring doc**

Create `docs/plugin-authoring.md`:

```markdown
# Plugin Authoring

`wkbench` plugins are executable programs that expose benchmark units through
the `wkbench.plugin/v1` frame protocol.

Phase 1 Go plugin entrypoint:

```go
package main

import (
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/plugins/demo/echo"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name:    "wkbench.demo",
		Version: "0.1.0",
		Units:   []contract.Unit{echo.Unit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
```

Plugin units use the same author-facing `contract.Unit` interface as existing
units. Output ports must declare metadata when they cross process boundaries:

```go
contract.PortDef{
	Name: "result",
	Type: "port.demo.echo/v1",
	Meta: contract.PortMeta{
		Boundary:   contract.PortBoundaryData,
		Transport:  contract.PortTransportInline,
		Reportable: true,
	},
}
```

Use a plugin from the host:

```bash
GOWORK=off go build -o /tmp/wkbench-demo-plugin ./plugins/demo/cmd/wkbench-demo-plugin
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin list-units
```
```

- [ ] **Step 3: Run docs-relevant commands**

Run:

```bash
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin list-units
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin validate -scenario ./examples/plugin-echo.yaml
```

Expected: `list-units` includes `demo.echo/v1`; validate prints `wkbench scenario is valid`.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/plugin-authoring.md
git commit -m "docs: document external plugin usage"
```

## Task 12: Final Verification

**Files:**
- No new files.

- [ ] **Step 1: Run focused plugin tests**

Run:

```bash
GOWORK=off go test ./benchkit/protocol ./benchkit/pluginhost ./sdk/go/wkbench/plugin ./plugins/demo/echo ./cmd/wkbench
```

Expected: PASS.

- [ ] **Step 2: Build demo plugin**

Run:

```bash
GOWORK=off go build -o /tmp/wkbench-demo-plugin ./plugins/demo/cmd/wkbench-demo-plugin
```

Expected: command exits 0 and `/tmp/wkbench-demo-plugin` exists.

- [ ] **Step 3: Run plugin scenario commands**

Run:

```bash
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin list-units
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin validate -scenario ./examples/plugin-echo.yaml
GOWORK=off go run ./cmd/wkbench -plugin /tmp/wkbench-demo-plugin run -scenario ./examples/plugin-echo.yaml
```

Expected:

- `list-units` includes `demo.echo/v1`;
- validate prints `wkbench scenario is valid`;
- run prints `wkbench run completed`.

- [ ] **Step 4: Run full test suite**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [ ] **Step 5: Run existing scenario validation**

Run:

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./examples/group-send.yaml
```

Expected: `wkbench scenario is valid`.

- [ ] **Step 6: Confirm git status**

Run:

```bash
git status --short
```

Expected: only intentional changes remain, or an empty status after all commits.
