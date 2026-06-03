package contract_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

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

func TestInputDecodesJSONShapedStruct(t *testing.T) {
	type message struct {
		Text  string `json:"text"`
		Count int    `json:"count"`
	}
	env := contract.NewTestRunEnv("run-1", "unit", map[string]any{
		"message": map[string]any{"text": "hello", "count": float64(3)},
	}, nil)

	got, err := contract.Input[message](env, "message")
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if got != (message{Text: "hello", Count: 3}) {
		t.Fatalf("message = %#v", got)
	}
}

func TestInputDecodesJSONShapedSlice(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "unit", map[string]any{
		"ids": []any{float64(1), float64(2), float64(3)},
	}, nil)

	got, err := contract.Input[[]int](env, "ids")
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("ids = %#v", got)
	}
}

func TestInputReportsJSONShapeDecodeErrors(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "unit", map[string]any{
		"ids": []any{"not-an-int"},
	}, nil)

	_, err := contract.Input[[]int](env, "ids")
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), `input "ids" has unexpected type []interface {}`) ||
		!strings.Contains(err.Error(), "decode as []int") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestTestRunEnvRecordsDurationObservations(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "traffic", nil, nil)
	env.ObserveDuration("sendack_latency", 10*time.Millisecond, nil)
	env.ObserveDuration("sendack_latency", 20*time.Millisecond, nil)

	values := env.DurationValues("sendack_latency")
	if len(values) != 2 || values[0] != 10*time.Millisecond || values[1] != 20*time.Millisecond {
		t.Fatalf("unexpected duration values: %#v", values)
	}
	values[0] = time.Hour
	if env.DurationValues("sendack_latency")[0] != 10*time.Millisecond {
		t.Fatal("DurationValues must return a copy")
	}
}

func TestTestRunEnvWorkerCountDefaultsToOneAndCanBeSet(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "traffic", nil, nil)
	if env.WorkerCount() != 1 {
		t.Fatalf("default WorkerCount = %d, want 1", env.WorkerCount())
	}
	env.SetWorkerCount(6)
	if env.WorkerCount() != 6 {
		t.Fatalf("WorkerCount = %d, want 6", env.WorkerCount())
	}
	env.SetWorkerCount(0)
	if env.WorkerCount() != 1 {
		t.Fatalf("non-positive WorkerCount = %d, want 1", env.WorkerCount())
	}
}

func TestTestRunEnvMetricSnapshotsPreserveLabelsAndAggregates(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "traffic", nil, nil)
	env.EmitCounter("attempt_total", 1, contract.Labels{"route": "a"})
	env.EmitCounter("attempt_total", 2, contract.Labels{"route": "a"})
	env.ObserveDuration("latency", time.Millisecond, contract.Labels{"route": "b"})
	env.ObserveDuration("latency", 3*time.Millisecond, contract.Labels{"route": "b"})

	snapshots := env.MetricSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %#v", snapshots)
	}
	counter := snapshots[0]
	if counter.Name != "attempt_total" || counter.Type != "counter" || counter.Count != 2 || counter.Sum != 3 || counter.Labels["route"] != "a" {
		t.Fatalf("counter snapshot = %#v", counter)
	}
	duration := snapshots[1]
	if duration.Name != "latency" || duration.Type != "duration" || duration.Count != 2 ||
		duration.Sum != 0.004 || duration.Min != 0.001 || duration.Max != 0.003 || duration.Labels["route"] != "b" {
		t.Fatalf("duration snapshot = %#v", duration)
	}
	snapshots[0].Labels["route"] = "mutated"
	if env.MetricSnapshots()[0].Labels["route"] != "a" {
		t.Fatal("MetricSnapshots must return cloned labels")
	}
}

func TestTestRunEnvWritesDeclaredArtifact(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "metrics", nil, nil)
	env.DeclareArtifacts([]contract.ArtifactDef{
		{Name: "metrics.jsonl", ContentType: "application/jsonl"},
	})

	w, err := env.OpenArtifact("metrics.jsonl")
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	payload := []byte("{\"ok\":true}\n")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close artifact: %v", err)
	}

	info := env.Artifacts()["metrics.jsonl"]
	if info.Path == "" {
		t.Fatal("artifact path is empty")
	}
	if info.ContentType != "application/jsonl" {
		t.Fatalf("ContentType = %q, want application/jsonl", info.ContentType)
	}
	if info.SizeBytes != int64(len(payload)) {
		t.Fatalf("SizeBytes = %d, want %d", info.SizeBytes, len(payload))
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
}

func TestTestRunEnvRejectsUndeclaredArtifact(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "metrics", nil, nil)

	_, err := env.OpenArtifact("metrics.jsonl")
	if err == nil {
		t.Fatal("expected undeclared artifact error")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error = %q, want not declared", err.Error())
	}
}

func TestTestRunEnvRejectsUnsafeDeclaredArtifactName(t *testing.T) {
	for _, name := range []string{".", "   ", " metrics.jsonl", "metrics.jsonl ", "metrics data.jsonl", "..", "../outside", "foo/bar", "foo\\bar"} {
		t.Run(name, func(t *testing.T) {
			env := contract.NewTestRunEnv("run-1", "metrics", nil, nil)
			env.DeclareArtifacts([]contract.ArtifactDef{{Name: name}})

			_, err := env.OpenArtifact(name)
			if err == nil {
				t.Fatal("expected unsafe artifact name error")
			}
			if !strings.Contains(err.Error(), "simple relative file name") {
				t.Fatalf("error = %q, want simple relative file name", err.Error())
			}
		})
	}
}

func TestTestRunEnvArtifactCloseRecordsMetadataWhenFileCloseFails(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "metrics", nil, nil)
	env.DeclareArtifacts([]contract.ArtifactDef{
		{Name: "metrics.jsonl", ContentType: "application/jsonl"},
	})

	w, err := env.OpenArtifact("metrics.jsonl")
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	payload := []byte("{\"ok\":true}\n")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	forceCloseArtifactFile(t, w)

	err = w.Close()
	if err == nil {
		t.Fatal("expected close error")
	}
	secondErr := w.Close()
	if secondErr == nil {
		t.Fatal("expected repeated Close to return the first close error")
	}
	if secondErr.Error() != err.Error() {
		t.Fatalf("second close error = %q, want %q", secondErr.Error(), err.Error())
	}
	info := env.Artifacts()["metrics.jsonl"]
	if info.Path == "" {
		t.Fatal("artifact path was not recorded")
	}
	if info.ContentType != "application/jsonl" {
		t.Fatalf("ContentType = %q, want application/jsonl", info.ContentType)
	}
	if info.SizeBytes != int64(len(payload)) {
		t.Fatalf("SizeBytes = %d, want %d", info.SizeBytes, len(payload))
	}
}

func TestBackgroundInterfacesCompile(t *testing.T) {
	var _ contract.BackgroundUnit = backgroundCompileUnit{}
	var _ contract.BackgroundTask = backgroundCompileTask{}
}

func forceCloseArtifactFile(t *testing.T, writer any) {
	t.Helper()
	value := reflect.ValueOf(writer)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		t.Fatalf("writer has unexpected shape %T", writer)
	}
	fileField := value.Elem().FieldByName("file")
	if !fileField.IsValid() || fileField.Kind() != reflect.Ptr {
		t.Fatalf("writer %T has no file pointer field", writer)
	}
	file := (*os.File)(unsafe.Pointer(fileField.Pointer()))
	if err := file.Close(); err != nil {
		t.Fatalf("force close artifact file: %v", err)
	}
}

type backgroundCompileUnit struct{}

func (backgroundCompileUnit) Definition() contract.Definition {
	return contract.Definition{Kind: "test.background/v1"}
}
func (backgroundCompileUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (backgroundCompileUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (backgroundCompileUnit) Run(context.Context, contract.RunEnv) error { return nil }
func (backgroundCompileUnit) Start(context.Context, contract.RunEnv) (contract.BackgroundTask, error) {
	return backgroundCompileTask{}, nil
}

type backgroundCompileTask struct{}

func (backgroundCompileTask) Done() <-chan error {
	ch := make(chan error)
	close(ch)
	return ch
}
func (backgroundCompileTask) Stop(context.Context) error { return nil }
