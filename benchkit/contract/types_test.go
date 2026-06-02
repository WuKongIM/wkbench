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
	for _, name := range []string{".", "   ", "..", "../outside", "foo/bar", "foo\\bar"} {
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
