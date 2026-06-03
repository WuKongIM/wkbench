package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
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

func TestManifestFromUnitsCopiesDefinitionSlices(t *testing.T) {
	source := contract.Definition{
		Kind: "demo.mutable/v1",
		Inputs: []contract.PortDef{{
			Name: "request",
			Type: "port.demo.request/v1",
			Meta: contract.PortMeta{
				Encodings:  []string{"json"},
				Operations: []string{"read"},
			},
		}},
		Outputs: []contract.PortDef{{
			Name: "response",
			Type: "port.demo.response/v1",
			Meta: contract.PortMeta{
				Encodings:  []string{"msgpack"},
				Operations: []string{"write"},
			},
		}},
		Metrics: []contract.MetricDef{{
			Name: "requests",
			Type: "counter",
		}},
		Artifacts: []contract.ArtifactDef{{
			Name:        "summary",
			ContentType: "application/json",
		}},
	}
	manifest := ManifestFromUnits("demo.plugin", "0.1.0", []contract.Unit{mutableUnit{def: source}})

	source.Inputs[0].Name = "changed-request"
	source.Inputs[0].Meta.Encodings[0] = "changed-json"
	source.Inputs[0].Meta.Operations[0] = "changed-read"
	source.Outputs[0].Name = "changed-response"
	source.Outputs[0].Meta.Encodings[0] = "changed-msgpack"
	source.Outputs[0].Meta.Operations[0] = "changed-write"
	source.Metrics[0].Name = "changed-requests"
	source.Artifacts[0].Name = "changed-summary"

	unit := manifest.Units[0]
	if unit.Inputs[0].Name != "request" || unit.Inputs[0].Meta.Encodings[0] != "json" || unit.Inputs[0].Meta.Operations[0] != "read" {
		t.Fatalf("input was not isolated from source mutation: %#v", unit.Inputs[0])
	}
	if unit.Outputs[0].Name != "response" || unit.Outputs[0].Meta.Encodings[0] != "msgpack" || unit.Outputs[0].Meta.Operations[0] != "write" {
		t.Fatalf("output was not isolated from source mutation: %#v", unit.Outputs[0])
	}
	if unit.Metrics[0].Name != "requests" {
		t.Fatalf("metric was not isolated from source mutation: %#v", unit.Metrics[0])
	}
	if unit.Artifacts[0].Name != "summary" {
		t.Fatalf("artifact was not isolated from source mutation: %#v", unit.Artifacts[0])
	}

	manifest.Units[0].Inputs[0].Name = "manifest-request"
	manifest.Units[0].Inputs[0].Meta.Encodings[0] = "manifest-json"
	manifest.Units[0].Inputs[0].Meta.Operations[0] = "manifest-read"
	manifest.Units[0].Outputs[0].Name = "manifest-response"
	manifest.Units[0].Outputs[0].Meta.Encodings[0] = "manifest-msgpack"
	manifest.Units[0].Outputs[0].Meta.Operations[0] = "manifest-write"
	manifest.Units[0].Metrics[0].Name = "manifest-requests"
	manifest.Units[0].Artifacts[0].Name = "manifest-summary"
	if source.Inputs[0].Name != "changed-request" || source.Inputs[0].Meta.Encodings[0] != "changed-json" || source.Inputs[0].Meta.Operations[0] != "changed-read" {
		t.Fatalf("source input was changed by manifest mutation: %#v", source.Inputs[0])
	}
	if source.Outputs[0].Name != "changed-response" || source.Outputs[0].Meta.Encodings[0] != "changed-msgpack" || source.Outputs[0].Meta.Operations[0] != "changed-write" {
		t.Fatalf("source output was changed by manifest mutation: %#v", source.Outputs[0])
	}
	if source.Metrics[0].Name != "changed-requests" {
		t.Fatalf("source metric was changed by manifest mutation: %#v", source.Metrics[0])
	}
	if source.Artifacts[0].Name != "changed-summary" {
		t.Fatalf("source artifact was changed by manifest mutation: %#v", source.Artifacts[0])
	}
}

func TestServerPlanAppliesWorkerCount(t *testing.T) {
	srv := newServer(Plugin{Name: "demo.plugin", Version: "0.1.0", Units: []contract.Unit{workerCountUnit{}}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "plan-1",
		Body: &protocol.Frame_PlanRequest{PlanRequest: &protocol.PlanRequest{
			UnitName:          "workers",
			Kind:              "demo.worker_count/v1",
			RunId:             "run-1",
			RunDurationMillis: 1000,
			WorkerCount:       7,
			SpecJson:          []byte(`{}`),
		}},
	}

	if err := srv.handlePlan(context.Background(), frame, frame.GetPlanRequest(), protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle plan: %v", err)
	}

	response := readServerTestFrame(t, &out)
	planResponse := response.GetPlanResponse()
	if planResponse == nil {
		t.Fatalf("response body = %T, want plan response", response.Body)
	}
	var plan contract.Plan
	if err := json.Unmarshal(planResponse.GetPlanJson(), &plan); err != nil {
		t.Fatalf("decode plan json: %v", err)
	}
	shard, ok := plan.Shards[0].(map[string]any)
	if !ok {
		t.Fatalf("plan shard = %#v", plan.Shards[0])
	}
	if shard["worker_count"] != float64(7) {
		t.Fatalf("worker_count = %#v, want 7", shard["worker_count"])
	}
}

func TestServerRunAppliesWorkerCount(t *testing.T) {
	srv := newServer(Plugin{Name: "demo.plugin", Version: "0.1.0", Units: []contract.Unit{workerCountUnit{}}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_RunRequest{RunRequest: &protocol.RunRequest{
			UnitName:          "workers",
			Kind:              "demo.worker_count/v1",
			RunId:             "run-1",
			RunDurationMillis: 1000,
			WorkerCount:       9,
			SpecJson:          []byte(`{}`),
		}},
	}

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle run: %v", err)
	}

	outputFrame := readServerTestFrame(t, &out)
	output := outputFrame.GetSetOutput()
	if output == nil {
		t.Fatalf("first response body = %T, want set output", outputFrame.Body)
	}
	var workerCount any
	if err := json.Unmarshal(output.GetValue().GetPayload(), &workerCount); err != nil {
		t.Fatalf("decode output json: %v", err)
	}
	if workerCount != float64(9) {
		t.Fatalf("worker_count output = %#v, want 9", workerCount)
	}
	terminalFrame := readServerTestFrame(t, &out)
	status := terminalFrame.GetTerminalStatus()
	if status == nil || !status.GetOk() {
		t.Fatalf("terminal status = %#v", status)
	}
}

func TestServerRunSeparatesRawAndReportPayload(t *testing.T) {
	srv := newServer(Plugin{Name: "demo.plugin", Version: "0.1.0", Units: []contract.Unit{secretReportUnit{}}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_RunRequest{RunRequest: &protocol.RunRequest{
			UnitName:          "secret",
			Kind:              "demo.secret_report/v1",
			RunId:             "run-1",
			RunDurationMillis: 1000,
			WorkerCount:       1,
			SpecJson:          []byte(`{}`),
		}},
	}

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle run: %v", err)
	}

	outputFrame := readServerTestFrame(t, &out)
	output := outputFrame.GetSetOutput()
	if output == nil {
		t.Fatalf("first response body = %T, want set output", outputFrame.Body)
	}
	value := output.GetValue()
	var raw map[string]any
	if err := json.Unmarshal(value.GetPayload(), &raw); err != nil {
		t.Fatalf("decode raw payload: %v", err)
	}
	if raw["secret"] != "token" || raw["public"] != "visible" {
		t.Fatalf("raw payload = %#v", raw)
	}
	var report map[string]any
	if err := json.Unmarshal(value.GetReportPayload(), &report); err != nil {
		t.Fatalf("decode report payload: %v", err)
	}
	if report["public"] != "visible" {
		t.Fatalf("report payload = %#v", report)
	}
	if _, ok := report["secret"]; ok {
		t.Fatalf("report payload exposed secret: %#v", report)
	}
}

func TestServerRunFallsBackToRawReportPayloadForPlainReportableOutput(t *testing.T) {
	srv := newServer(Plugin{Name: "demo.plugin", Version: "0.1.0", Units: []contract.Unit{plainReportUnit{}}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_RunRequest{RunRequest: &protocol.RunRequest{
			UnitName:          "plain",
			Kind:              "demo.plain_report/v1",
			RunId:             "run-1",
			RunDurationMillis: 1000,
			WorkerCount:       1,
			SpecJson:          []byte(`{}`),
		}},
	}

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle run: %v", err)
	}

	outputFrame := readServerTestFrame(t, &out)
	value := outputFrame.GetSetOutput().GetValue()
	if !bytes.Equal(value.GetPayload(), value.GetReportPayload()) {
		t.Fatalf("report payload = %s, want raw payload %s", value.GetReportPayload(), value.GetPayload())
	}
}

func TestServerRunSendsMetricFlush(t *testing.T) {
	srv := newServer(Plugin{Name: "demo.plugin", Version: "0.1.0", Units: []contract.Unit{metricFlushUnit{}}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_RunRequest{RunRequest: &protocol.RunRequest{
			UnitName:          "metrics",
			Kind:              "demo.metric_flush/v1",
			RunId:             "run-1",
			RunDurationMillis: 1000,
			WorkerCount:       1,
			SpecJson:          []byte(`{}`),
		}},
	}

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle run: %v", err)
	}

	metricFrame := readServerTestFrame(t, &out)
	flush := metricFrame.GetMetricFlush()
	if flush == nil {
		t.Fatalf("first response body = %T, want metric flush", metricFrame.Body)
	}
	if len(flush.GetMetrics()) != 2 {
		t.Fatalf("metrics = %#v", flush.GetMetrics())
	}
	counter := flush.GetMetrics()[0]
	if counter.GetName() != "attempt_total" || counter.GetType() != "counter" || counter.GetCount() != 2 || counter.GetSum() != 3 || counter.GetLabels()["route"] != "a" {
		t.Fatalf("counter snapshot = %#v", counter)
	}
	duration := flush.GetMetrics()[1]
	if duration.GetName() != "latency" || duration.GetType() != "duration" || duration.GetCount() != 2 ||
		math.Abs(duration.GetSum()-0.004) > 0.0000001 ||
		math.Abs(duration.GetMin()-0.001) > 0.0000001 ||
		math.Abs(duration.GetMax()-0.003) > 0.0000001 {
		t.Fatalf("duration snapshot = %#v", duration)
	}
	terminalFrame := readServerTestFrame(t, &out)
	if status := terminalFrame.GetTerminalStatus(); status == nil || !status.GetOk() {
		t.Fatalf("terminal status = %#v", status)
	}
}

func readServerTestFrame(t *testing.T, buf *bytes.Buffer) *protocol.Frame {
	t.Helper()
	frame, err := protocol.NewFrameReader(buf, 16<<20).ReadFrame()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return frame
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

type mutableUnit struct {
	def contract.Definition
}

func (u mutableUnit) Definition() contract.Definition { return u.def }
func (mutableUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (u mutableUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}
func (mutableUnit) Run(ctx context.Context, env contract.RunEnv) error { return nil }

type workerCountUnit struct{}

func (workerCountUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.worker_count/v1",
		Outputs: []contract.PortDef{{
			Name: "worker_count",
			Type: "port.demo.worker_count/v1",
		}},
	}
}
func (workerCountUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (workerCountUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{
		UnitName: env.UnitName(),
		Shards: []any{map[string]any{
			"worker_count": env.WorkerCount(),
		}},
	}, nil
}
func (workerCountUnit) Run(ctx context.Context, env contract.RunEnv) error {
	return env.SetOutput("worker_count", env.WorkerCount())
}

type secretReportUnit struct{}

func (secretReportUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.secret_report/v1",
		Outputs: []contract.PortDef{{
			Name: "result",
			Type: "port.demo.secret_report/v1",
			Meta: contract.PortMeta{Reportable: true},
		}},
	}
}
func (secretReportUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (secretReportUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (secretReportUnit) Run(ctx context.Context, env contract.RunEnv) error {
	return env.SetOutput("result", secretReportValue{Secret: "token", Public: "visible"})
}

type secretReportValue struct {
	Secret string `json:"secret"`
	Public string `json:"public"`
}

func (v secretReportValue) ReportOutput() any {
	return map[string]any{"public": v.Public}
}

type plainReportUnit struct{}

func (plainReportUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.plain_report/v1",
		Outputs: []contract.PortDef{{
			Name: "result",
			Type: "port.demo.plain_report/v1",
			Meta: contract.PortMeta{Reportable: true},
		}},
	}
}
func (plainReportUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (plainReportUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (plainReportUnit) Run(ctx context.Context, env contract.RunEnv) error {
	return env.SetOutput("result", map[string]any{"message": "visible"})
}

type metricFlushUnit struct{}

func (metricFlushUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.metric_flush/v1",
		Metrics: []contract.MetricDef{
			{Name: "attempt_total", Type: "counter"},
			{Name: "latency", Type: "duration"},
		},
	}
}
func (metricFlushUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (metricFlushUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (metricFlushUnit) Run(ctx context.Context, env contract.RunEnv) error {
	labels := contract.Labels{"route": "a"}
	env.EmitCounter("attempt_total", 1, labels)
	env.EmitCounter("attempt_total", 2, labels)
	env.ObserveDuration("latency", time.Millisecond, nil)
	env.ObserveDuration("latency", 3*time.Millisecond, nil)
	return nil
}
