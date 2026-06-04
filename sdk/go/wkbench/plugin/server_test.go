package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), nil, protocol.NewFrameWriter(&out)); err != nil {
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

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), nil, protocol.NewFrameWriter(&out)); err != nil {
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

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), nil, protocol.NewFrameWriter(&out)); err != nil {
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

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), nil, protocol.NewFrameWriter(&out)); err != nil {
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

func TestServerRunStreamsArtifacts(t *testing.T) {
	srv := newServer(Plugin{Name: "demo.plugin", Version: "0.1.0", Units: []contract.Unit{artifactRunUnit{}}})
	toHostReader, toHostWriter := io.Pipe()
	toPluginReader, toPluginWriter := io.Pipe()
	serverErr := make(chan error, 1)
	frame := &protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_RunRequest{RunRequest: &protocol.RunRequest{
			UnitName:          "artifact",
			Kind:              "demo.artifact/v1",
			RunId:             "run-1",
			RunDurationMillis: 1000,
			WorkerCount:       1,
			SpecJson:          []byte(`{}`),
		}},
	}

	go func() {
		err := srv.handleRun(
			context.Background(),
			frame,
			frame.GetRunRequest(),
			protocol.NewFrameReader(toPluginReader, 16<<20),
			protocol.NewFrameWriter(toHostWriter),
		)
		_ = toHostWriter.Close()
		serverErr <- err
	}()

	hostReader := protocol.NewFrameReader(toHostReader, 16<<20)
	hostWriter := protocol.NewFrameWriter(toPluginWriter)
	openFrame := readServerPipeFrame(t, hostReader)
	open := openFrame.GetArtifactOpen()
	if open == nil || open.GetName() != "metrics.jsonl" {
		t.Fatalf("first frame = %#v, want artifact open", openFrame)
	}
	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_ArtifactOpened{ArtifactOpened: &protocol.ArtifactOpened{
			Name:   "metrics.jsonl",
			Handle: "artifact-1",
		}},
	}); err != nil {
		t.Fatalf("write opened response: %v", err)
	}

	var chunks bytes.Buffer
	chunkFrame := readServerPipeFrame(t, hostReader)
	if chunkFrame.GetRequestId() != "run-1" {
		t.Fatalf("artifact chunk request id = %q, want run-1", chunkFrame.GetRequestId())
	}
	chunk := chunkFrame.GetArtifactChunk()
	if chunk == nil || chunk.GetHandle() != "artifact-1" || chunk.GetSequence() != 1 {
		t.Fatalf("first chunk = %#v", chunk)
	}
	chunks.Write(chunk.GetData())
	chunkFrame = readServerPipeFrame(t, hostReader)
	if chunkFrame.GetRequestId() != "run-1" {
		t.Fatalf("artifact chunk request id = %q, want run-1", chunkFrame.GetRequestId())
	}
	chunk = chunkFrame.GetArtifactChunk()
	if chunk == nil || chunk.GetHandle() != "artifact-1" || chunk.GetSequence() != 2 {
		t.Fatalf("second chunk = %#v", chunk)
	}
	chunks.Write(chunk.GetData())
	if got := chunks.String(); got != "one\ntwo\n" {
		t.Fatalf("artifact chunks = %q", got)
	}

	closeFrame := readServerPipeFrame(t, hostReader)
	closeArtifact := closeFrame.GetArtifactClose()
	if closeArtifact == nil || closeArtifact.GetHandle() != "artifact-1" {
		t.Fatalf("close frame = %#v", closeFrame)
	}
	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_ArtifactClosed{ArtifactClosed: &protocol.ArtifactClosed{
			Handle:    "artifact-1",
			SizeBytes: int64(chunks.Len()),
		}},
	}); err != nil {
		t.Fatalf("write closed response: %v", err)
	}

	outputFrame := readServerPipeFrame(t, hostReader)
	if output := outputFrame.GetSetOutput(); output == nil || output.GetName() != "result" {
		t.Fatalf("output frame = %#v", outputFrame)
	}
	terminalFrame := readServerPipeFrame(t, hostReader)
	if status := terminalFrame.GetTerminalStatus(); status == nil || !status.GetOk() {
		t.Fatalf("terminal status = %#v", status)
	}
	_ = toPluginWriter.Close()
	if err := <-serverErr; err != nil {
		t.Fatalf("handle run: %v", err)
	}
}

func TestServerRunTypedStructInputFromRPC(t *testing.T) {
	srv := newServer(Plugin{Name: "demo.plugin", Version: "0.1.0", Units: []contract.Unit{typedInputUnit{}}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "run-1",
		Body: &protocol.Frame_RunRequest{RunRequest: &protocol.RunRequest{
			UnitName:          "typed",
			Kind:              "demo.typed_input/v1",
			RunId:             "run-1",
			RunDurationMillis: 1000,
			WorkerCount:       1,
			SpecJson:          []byte(`{}`),
			Inputs: map[string]*protocol.PortValue{
				"request": {
					Encoding:  "json",
					Transport: string(contract.PortTransportInline),
					Payload:   []byte(`{"message":"hello","count":3}`),
				},
			},
		}},
	}

	if err := srv.handleRun(context.Background(), frame, frame.GetRunRequest(), nil, protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle run: %v", err)
	}

	outputFrame := readServerTestFrame(t, &out)
	output := outputFrame.GetSetOutput()
	if output == nil {
		t.Fatalf("first response body = %T, want set output", outputFrame.Body)
	}
	var response map[string]any
	if err := json.Unmarshal(output.GetValue().GetPayload(), &response); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if response["summary"] != "hello:3" {
		t.Fatalf("response = %#v", response)
	}
	terminalFrame := readServerTestFrame(t, &out)
	if status := terminalFrame.GetTerminalStatus(); status == nil || !status.GetOk() {
		t.Fatalf("terminal status = %#v", status)
	}
}

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

func TestServerStartRejectsNilBackgroundTask(t *testing.T) {
	unit := &serverBackgroundUnit{returnNilTask: true}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	var out bytes.Buffer
	frame := &protocol.Frame{
		RequestId: "start-nil",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			SpecJson: []byte(`{}`),
		}},
	}

	if err := srv.handleStart(context.Background(), frame, frame.GetStartRequest(), nil, protocol.NewFrameWriter(&out)); err != nil {
		t.Fatalf("handle start: %v", err)
	}
	response := readServerTestFrame(t, &out)
	if response.GetStartResponse() != nil {
		t.Fatalf("response = %#v, want error", response)
	}
	rpcErr := response.GetError()
	if rpcErr == nil || rpcErr.GetCode() != "RUN_ERROR" {
		t.Fatalf("error = %#v, want RUN_ERROR", rpcErr)
	}
}

func TestServerStopFlushesBackgroundOutputsAndMetrics(t *testing.T) {
	unit := &serverBackgroundUnit{writeFinalState: true}
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
	taskID := readServerTestFrame(t, &out).GetStartResponse().GetTaskId()

	out.Reset()
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	if err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), writer); err != nil {
		t.Fatalf("handle stop: %v", err)
	}

	outputFrame := readServerTestFrame(t, &out)
	if outputFrame.GetRequestId() != "start-1" {
		t.Fatalf("output request id = %q, want start-1", outputFrame.GetRequestId())
	}
	output := outputFrame.GetSetOutput()
	if output == nil || output.GetName() != "summary" {
		t.Fatalf("output frame = %#v", outputFrame)
	}
	var summary map[string]any
	if err := json.Unmarshal(output.GetValue().GetPayload(), &summary); err != nil {
		t.Fatalf("decode output payload: %v", err)
	}
	if summary["stopped"] != true {
		t.Fatalf("summary = %#v", summary)
	}
	metricFrame := readServerTestFrame(t, &out)
	if metricFrame.GetRequestId() != "start-1" {
		t.Fatalf("metric request id = %q, want start-1", metricFrame.GetRequestId())
	}
	flush := metricFrame.GetMetricFlush()
	if flush == nil || len(flush.GetMetrics()) != 1 || flush.GetMetrics()[0].GetName() != "background_ticks" || flush.GetMetrics()[0].GetCount() != 1 || flush.GetMetrics()[0].GetSum() != 3 {
		t.Fatalf("metric flush = %#v", flush)
	}
	stopResponse := readServerTestFrame(t, &out)
	if stopResponse.GetRequestId() != "stop-1" || stopResponse.GetStopResponse() == nil {
		t.Fatalf("stop response = %#v", stopResponse)
	}
}

func TestServerStopFailureLeavesTaskRetryable(t *testing.T) {
	unit := &serverBackgroundUnit{
		writeFinalState:     true,
		stopErrorsRemaining: 1,
	}
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
	taskID := readServerTestFrame(t, &out).GetStartResponse().GetTaskId()

	out.Reset()
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	if err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), writer); err != nil {
		t.Fatalf("first handle stop: %v", err)
	}
	firstStop := readServerTestFrame(t, &out)
	if rpcErr := firstStop.GetError(); rpcErr == nil || rpcErr.GetCode() != "RUN_ERROR" {
		t.Fatalf("first stop frame = %#v, want RUN_ERROR", firstStop)
	}

	out.Reset()
	stopFrame.RequestId = "stop-2"
	if err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), writer); err != nil {
		t.Fatalf("second handle stop: %v", err)
	}
	outputFrame := readServerTestFrame(t, &out)
	if outputFrame.GetRequestId() != "start-1" || outputFrame.GetSetOutput() == nil {
		t.Fatalf("retry output frame = %#v", outputFrame)
	}
	metricFrame := readServerTestFrame(t, &out)
	if metricFrame.GetRequestId() != "start-1" || metricFrame.GetMetricFlush() == nil {
		t.Fatalf("retry metric frame = %#v", metricFrame)
	}
	stopResponse := readServerTestFrame(t, &out)
	if stopResponse.GetRequestId() != "stop-2" || stopResponse.GetStopResponse() == nil {
		t.Fatalf("retry stop response = %#v", stopResponse)
	}
	if unit.stopCalls != 2 {
		t.Fatalf("stop calls = %d, want 2", unit.stopCalls)
	}
}

func TestServerStopRawOutputFlushFailureLeavesTaskRetryable(t *testing.T) {
	testServerStopOutputFlushFailureLeavesTaskRetryable(t, func(unit *serverBackgroundUnit) {
		unit.badOutputStopsRemaining = 1
	})
}

func TestServerStopReportOutputFlushFailureLeavesTaskRetryable(t *testing.T) {
	testServerStopOutputFlushFailureLeavesTaskRetryable(t, func(unit *serverBackgroundUnit) {
		unit.badReportStopsRemaining = 1
	})
}

func TestServerServeStopRawOutputFlushFailureLeavesTaskRetryable(t *testing.T) {
	testServerServeStopOutputFlushFailureLeavesTaskRetryable(t, func(unit *serverBackgroundUnit) {
		unit.badOutputStopsRemaining = 1
	})
}

func TestServerServeStopReportOutputFlushFailureLeavesTaskRetryable(t *testing.T) {
	testServerServeStopOutputFlushFailureLeavesTaskRetryable(t, func(unit *serverBackgroundUnit) {
		unit.badReportStopsRemaining = 1
	})
}

func testServerStopOutputFlushFailureLeavesTaskRetryable(t *testing.T, configure func(*serverBackgroundUnit)) {
	t.Helper()
	unit := &serverBackgroundUnit{writeFinalState: true}
	configure(unit)
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
	taskID := readServerTestFrame(t, &out).GetStartResponse().GetTaskId()

	out.Reset()
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	if err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), writer); err != nil {
		t.Fatalf("first handle stop: %v", err)
	}
	firstStop := readServerTestFrame(t, &out)
	if firstStop.GetStopResponse() != nil {
		t.Fatalf("first stop frame = %#v, want error frame", firstStop)
	}
	if firstStop.GetRequestId() != "stop-1" {
		t.Fatalf("first stop error request id = %q, want stop-1", firstStop.GetRequestId())
	}
	if rpcErr := firstStop.GetError(); rpcErr == nil || rpcErr.GetCode() != "RUN_ERROR" {
		t.Fatalf("first stop frame = %#v, want RUN_ERROR", firstStop)
	}

	out.Reset()
	stopFrame.RequestId = "stop-2"
	if err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), writer); err != nil {
		t.Fatalf("second handle stop: %v", err)
	}
	outputFrame := readServerTestFrame(t, &out)
	if outputFrame.GetRequestId() != "start-1" || outputFrame.GetSetOutput() == nil {
		t.Fatalf("retry output frame = %#v", outputFrame)
	}
	metricFrame := readServerTestFrame(t, &out)
	if metricFrame.GetRequestId() != "start-1" || metricFrame.GetMetricFlush() == nil {
		t.Fatalf("retry metric frame = %#v", metricFrame)
	}
	stopResponse := readServerTestFrame(t, &out)
	if stopResponse.GetRequestId() != "stop-2" || stopResponse.GetStopResponse() == nil {
		t.Fatalf("retry stop response = %#v", stopResponse)
	}
	if unit.stopCalls != 2 {
		t.Fatalf("stop calls = %d, want 2", unit.stopCalls)
	}
}

func testServerServeStopOutputFlushFailureLeavesTaskRetryable(t *testing.T, configure func(*serverBackgroundUnit)) {
	t.Helper()
	unit := &serverBackgroundUnit{writeFinalState: true}
	configure(unit)
	toServerReader, toServerWriter := io.Pipe()
	fromServerReader, fromServerWriter := io.Pipe()
	serveErr := make(chan error, 1)
	go func() {
		err := Serve(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}}, toServerReader, fromServerWriter)
		_ = fromServerWriter.Close()
		serveErr <- err
	}()

	hostWriter := protocol.NewFrameWriter(toServerWriter)
	hostReader := protocol.NewFrameReader(fromServerReader, 16<<20)
	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}); err != nil {
		t.Fatalf("write start: %v", err)
	}
	startFrame := readServerPipeFrame(t, hostReader)
	taskID := startFrame.GetStartResponse().GetTaskId()
	if taskID == "" {
		t.Fatalf("start frame = %#v", startFrame)
	}

	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}); err != nil {
		t.Fatalf("write first stop: %v", err)
	}
	firstStop := readServerPipeFrame(t, hostReader)
	if firstStop.GetRequestId() != "stop-1" {
		t.Fatalf("first stop request id = %q, want stop-1", firstStop.GetRequestId())
	}
	if rpcErr := firstStop.GetError(); rpcErr == nil || rpcErr.GetCode() != "RUN_ERROR" {
		t.Fatalf("first stop frame = %#v, want RUN_ERROR", firstStop)
	}
	if firstStop.GetStopResponse() != nil {
		t.Fatalf("first stop frame = %#v, want no stop response", firstStop)
	}

	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "stop-2",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}); err != nil {
		t.Fatalf("write second stop: %v", err)
	}
	outputFrame := readServerPipeFrame(t, hostReader)
	if outputFrame.GetRequestId() != "start-1" || outputFrame.GetSetOutput() == nil {
		t.Fatalf("retry output frame = %#v", outputFrame)
	}
	metricFrame := readServerPipeFrame(t, hostReader)
	if metricFrame.GetRequestId() != "start-1" || metricFrame.GetMetricFlush() == nil {
		t.Fatalf("retry metric frame = %#v", metricFrame)
	}
	stopResponse := readServerPipeFrame(t, hostReader)
	if stopResponse.GetRequestId() != "stop-2" || stopResponse.GetStopResponse() == nil {
		t.Fatalf("retry stop response = %#v", stopResponse)
	}
	if unit.stopCalls != 2 {
		t.Fatalf("stop calls = %d, want 2", unit.stopCalls)
	}
	_ = toServerWriter.Close()
	if err := <-serveErr; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestServerStartStopStreamsBackgroundArtifacts(t *testing.T) {
	unit := &serverBackgroundUnit{writeArtifacts: true}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	toHostReader, toHostWriter := io.Pipe()
	toPluginReader, toPluginWriter := io.Pipe()
	serverErr := make(chan error, 1)
	startFrame := &protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}

	go func() {
		err := srv.handleStart(
			context.Background(),
			startFrame,
			startFrame.GetStartRequest(),
			protocol.NewFrameReader(toPluginReader, 16<<20),
			protocol.NewFrameWriter(toHostWriter),
		)
		serverErr <- err
	}()

	hostReader := protocol.NewFrameReader(toHostReader, 16<<20)
	hostWriter := protocol.NewFrameWriter(toPluginWriter)
	openFrame := readServerPipeFrame(t, hostReader)
	open := openFrame.GetArtifactOpen()
	if openFrame.GetRequestId() != "start-1" {
		t.Fatalf("artifact open request id = %q, want start-1", openFrame.GetRequestId())
	}
	if open == nil || open.GetName() != "background.jsonl" {
		t.Fatalf("open frame = %#v", openFrame)
	}
	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_ArtifactOpened{ArtifactOpened: &protocol.ArtifactOpened{
			Name:   "background.jsonl",
			Handle: "artifact-1",
		}},
	}); err != nil {
		t.Fatalf("write opened response: %v", err)
	}
	chunkFrame := readServerPipeFrame(t, hostReader)
	if chunkFrame.GetRequestId() != "start-1" {
		t.Fatalf("artifact chunk request id = %q, want start-1", chunkFrame.GetRequestId())
	}
	chunk := chunkFrame.GetArtifactChunk()
	if chunk == nil || chunk.GetHandle() != "artifact-1" || string(chunk.GetData()) != "start\n" {
		t.Fatalf("chunk = %#v", chunk)
	}
	closeFrame := readServerPipeFrame(t, hostReader)
	if closeFrame.GetRequestId() != "start-1" {
		t.Fatalf("artifact close request id = %q, want start-1", closeFrame.GetRequestId())
	}
	if closeArtifact := closeFrame.GetArtifactClose(); closeArtifact == nil || closeArtifact.GetHandle() != "artifact-1" {
		t.Fatalf("close frame = %#v", closeFrame)
	}
	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_ArtifactClosed{ArtifactClosed: &protocol.ArtifactClosed{
			Handle:    "artifact-1",
			SizeBytes: int64(len("start\n")),
		}},
	}); err != nil {
		t.Fatalf("write closed response: %v", err)
	}
	startResponse := readServerPipeFrame(t, hostReader)
	taskID := startResponse.GetStartResponse().GetTaskId()
	if taskID == "" {
		t.Fatalf("start response = %#v", startResponse)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("handle start: %v", err)
	}

	serverErr = make(chan error, 1)
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	go func() {
		err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), protocol.NewFrameWriter(toHostWriter))
		_ = toHostWriter.Close()
		serverErr <- err
	}()
	openFrame = readServerPipeFrame(t, hostReader)
	open = openFrame.GetArtifactOpen()
	if openFrame.GetRequestId() != "start-1" {
		t.Fatalf("stop artifact open request id = %q, want start-1", openFrame.GetRequestId())
	}
	if open == nil || open.GetName() != "background.jsonl" {
		t.Fatalf("stop open frame = %#v", openFrame)
	}
	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_ArtifactOpened{ArtifactOpened: &protocol.ArtifactOpened{
			Name:   "background.jsonl",
			Handle: "artifact-2",
		}},
	}); err != nil {
		t.Fatalf("write stop opened response: %v", err)
	}
	chunkFrame = readServerPipeFrame(t, hostReader)
	if chunkFrame.GetRequestId() != "start-1" {
		t.Fatalf("stop artifact chunk request id = %q, want start-1", chunkFrame.GetRequestId())
	}
	chunk = chunkFrame.GetArtifactChunk()
	if chunk == nil || chunk.GetHandle() != "artifact-2" || string(chunk.GetData()) != "stop\n" {
		t.Fatalf("stop chunk = %#v", chunk)
	}
	closeFrame = readServerPipeFrame(t, hostReader)
	if closeFrame.GetRequestId() != "start-1" {
		t.Fatalf("stop artifact close request id = %q, want start-1", closeFrame.GetRequestId())
	}
	if closeArtifact := closeFrame.GetArtifactClose(); closeArtifact == nil || closeArtifact.GetHandle() != "artifact-2" {
		t.Fatalf("stop close frame = %#v", closeFrame)
	}
	if err := hostWriter.WriteFrame(&protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_ArtifactClosed{ArtifactClosed: &protocol.ArtifactClosed{
			Handle:    "artifact-2",
			SizeBytes: int64(len("stop\n")),
		}},
	}); err != nil {
		t.Fatalf("write stop closed response: %v", err)
	}
	stopResponse := readServerPipeFrame(t, hostReader)
	if stopResponse.GetStopResponse() == nil {
		t.Fatalf("stop response = %#v", stopResponse)
	}
	_ = toPluginWriter.Close()
	if err := <-serverErr; err != nil {
		t.Fatalf("handle stop: %v", err)
	}
}

func TestServerBackgroundTaskDoneErrorEmitsFatalEvent(t *testing.T) {
	unit := &serverBackgroundUnit{done: make(chan error, 1)}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	toHostReader, toHostWriter := io.Pipe()
	serverErr := make(chan error, 1)
	startFrame := &protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}
	go func() {
		err := srv.handleStart(context.Background(), startFrame, startFrame.GetStartRequest(), nil, protocol.NewFrameWriter(toHostWriter))
		serverErr <- err
	}()

	hostReader := protocol.NewFrameReader(toHostReader, 16<<20)
	taskID := readServerPipeFrame(t, hostReader).GetStartResponse().GetTaskId()
	if err := <-serverErr; err != nil {
		t.Fatalf("handle start: %v", err)
	}

	unit.done <- fmt.Errorf("collector failed")
	eventFrame := readServerPipeFrame(t, hostReader)
	event := eventFrame.GetBackgroundEvent()
	if event == nil {
		t.Fatalf("event frame = %#v", eventFrame)
	}
	if event.GetTaskId() != taskID || event.GetEvent() != "fatal_error" {
		t.Fatalf("event = %#v", event)
	}
	if event.GetError() == nil || event.GetError().GetMessage() != "collector failed" {
		t.Fatalf("event error = %#v", event.GetError())
	}
	_ = toHostWriter.Close()
}

func TestServerBackgroundTaskDoneNilEmitsCompletedEvent(t *testing.T) {
	unit := &serverBackgroundUnit{done: make(chan error, 1)}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	toHostReader, toHostWriter := io.Pipe()
	serverErr := make(chan error, 1)
	startFrame := &protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}
	go func() {
		err := srv.handleStart(context.Background(), startFrame, startFrame.GetStartRequest(), nil, protocol.NewFrameWriter(toHostWriter))
		serverErr <- err
	}()

	hostReader := protocol.NewFrameReader(toHostReader, 16<<20)
	taskID := readServerPipeFrame(t, hostReader).GetStartResponse().GetTaskId()
	if err := <-serverErr; err != nil {
		t.Fatalf("handle start: %v", err)
	}

	unit.done <- nil
	eventFrame := readServerPipeFrame(t, hostReader)
	event := eventFrame.GetBackgroundEvent()
	if event == nil {
		t.Fatalf("event frame = %#v", eventFrame)
	}
	if event.GetTaskId() != taskID || event.GetEvent() != "completed" {
		t.Fatalf("event = %#v", event)
	}
	if event.GetError() != nil {
		t.Fatalf("event error = %#v, want nil", event.GetError())
	}
	_ = toHostWriter.Close()
}

func TestServerStopAfterBackgroundFatalEventFlushesFinalState(t *testing.T) {
	testServerStopAfterBackgroundEventFlushesFinalState(t, fmt.Errorf("collector failed"), "fatal_error")
}

func TestServerStopAfterBackgroundCompletedEventFlushesFinalState(t *testing.T) {
	testServerStopAfterBackgroundEventFlushesFinalState(t, nil, "completed")
}

func TestServerBackgroundEventDuringStopIsNotDropped(t *testing.T) {
	unit := &serverBackgroundUnit{
		done:            make(chan error, 1),
		doneOnStop:      true,
		doneOnStopErr:   fmt.Errorf("collector failed"),
		writeFinalState: true,
	}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	toHostReader, toHostWriter := io.Pipe()
	frameCh := make(chan *protocol.Frame, 8)
	readErr := make(chan error, 1)
	go func() {
		reader := protocol.NewFrameReader(toHostReader, 16<<20)
		for {
			frame, err := reader.ReadFrame()
			if err != nil {
				readErr <- err
				return
			}
			frameCh <- frame
		}
	}()

	startFrame := &protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}
	if err := srv.handleStart(context.Background(), startFrame, startFrame.GetStartRequest(), nil, protocol.NewFrameWriter(toHostWriter)); err != nil {
		t.Fatalf("handle start: %v", err)
	}
	startResponse := readServerFrameFromChannel(t, frameCh, readErr)
	taskID := startResponse.GetStartResponse().GetTaskId()
	if taskID == "" {
		t.Fatalf("start response = %#v", startResponse)
	}

	stopErr := make(chan error, 1)
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	go func() {
		stopErr <- srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), protocol.NewFrameWriter(toHostWriter))
	}()

	var sawEvent, sawOutput, sawMetric, sawStop bool
	for !(sawEvent && sawOutput && sawMetric && sawStop) {
		frame := readServerFrameFromChannel(t, frameCh, readErr)
		switch {
		case frame.GetBackgroundEvent() != nil:
			event := frame.GetBackgroundEvent()
			if event.GetTaskId() != taskID || event.GetEvent() != "fatal_error" {
				t.Fatalf("background event = %#v", event)
			}
			if event.GetError() == nil || event.GetError().GetMessage() != "collector failed" {
				t.Fatalf("background event error = %#v", event.GetError())
			}
			sawEvent = true
		case frame.GetSetOutput() != nil:
			if frame.GetRequestId() != "start-1" {
				t.Fatalf("output request id = %q, want start-1", frame.GetRequestId())
			}
			sawOutput = true
		case frame.GetMetricFlush() != nil:
			if frame.GetRequestId() != "start-1" {
				t.Fatalf("metric request id = %q, want start-1", frame.GetRequestId())
			}
			sawMetric = true
		case frame.GetStopResponse() != nil:
			if frame.GetRequestId() != "stop-1" {
				t.Fatalf("stop response request id = %q, want stop-1", frame.GetRequestId())
			}
			sawStop = true
		default:
			t.Fatalf("unexpected frame = %#v", frame)
		}
	}
	if err := <-stopErr; err != nil {
		t.Fatalf("handle stop: %v", err)
	}
	_ = toHostWriter.Close()
}

func TestServerBackgroundEventBeforeStopCanBeReadAfterStop(t *testing.T) {
	unit := &serverBackgroundUnit{
		done:            make(chan error, 1),
		writeFinalState: true,
	}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	toHostReader, toHostWriter := io.Pipe()
	frameCh := make(chan *protocol.Frame, 8)
	readErr := make(chan error, 1)
	go func() {
		reader := protocol.NewFrameReader(toHostReader, 16<<20)
		for {
			frame, err := reader.ReadFrame()
			if err != nil {
				readErr <- err
				return
			}
			frameCh <- frame
		}
	}()

	startFrame := &protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}
	if err := srv.handleStart(context.Background(), startFrame, startFrame.GetStartRequest(), nil, protocol.NewFrameWriter(toHostWriter)); err != nil {
		t.Fatalf("handle start: %v", err)
	}
	startResponse := readServerFrameFromChannel(t, frameCh, readErr)
	taskID := startResponse.GetStartResponse().GetTaskId()
	if taskID == "" {
		t.Fatalf("start response = %#v", startResponse)
	}

	unit.done <- fmt.Errorf("collector failed")
	stopErr := make(chan error, 1)
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	go func() {
		stopErr <- srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), protocol.NewFrameWriter(toHostWriter))
	}()

	var sawEvent, sawOutput, sawMetric, sawStop bool
	for !(sawEvent && sawOutput && sawMetric && sawStop) {
		frame := readServerFrameFromChannel(t, frameCh, readErr)
		switch {
		case frame.GetBackgroundEvent() != nil:
			event := frame.GetBackgroundEvent()
			if event.GetTaskId() != taskID || event.GetEvent() != "fatal_error" {
				t.Fatalf("background event = %#v", event)
			}
			if event.GetError() == nil || event.GetError().GetMessage() != "collector failed" {
				t.Fatalf("background event error = %#v", event.GetError())
			}
			sawEvent = true
		case frame.GetSetOutput() != nil:
			if frame.GetRequestId() != "start-1" {
				t.Fatalf("output request id = %q, want start-1", frame.GetRequestId())
			}
			sawOutput = true
		case frame.GetMetricFlush() != nil:
			if frame.GetRequestId() != "start-1" {
				t.Fatalf("metric request id = %q, want start-1", frame.GetRequestId())
			}
			sawMetric = true
		case frame.GetStopResponse() != nil:
			if frame.GetRequestId() != "stop-1" {
				t.Fatalf("stop response request id = %q, want stop-1", frame.GetRequestId())
			}
			sawStop = true
		default:
			t.Fatalf("unexpected frame = %#v", frame)
		}
	}
	if err := <-stopErr; err != nil {
		t.Fatalf("handle stop: %v", err)
	}
	_ = toHostWriter.Close()
}

func TestServerStopFlushesReadyDoneWhenMonitorHasNotRun(t *testing.T) {
	unit := &serverBackgroundUnit{
		done:            make(chan error, 1),
		writeFinalState: true,
	}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	var out bytes.Buffer
	writer := protocol.NewFrameWriter(&out)
	runReq := &protocol.RunRequest{
		UnitName: "bg",
		Kind:     "test.server_background/v1",
		RunId:    "run-1",
		SpecJson: []byte(`{}`),
	}
	env := newRemoteRunEnv("start-1", runReq, nil, map[string]any{}, unit.Definition().Artifacts, nil, func(frame *protocol.Frame) error {
		return srv.writeFrame(writer, frame)
	})
	unit.env = env
	task := serverBackgroundTask{unit: unit}
	record := srv.storeBackgroundTask("start-1", "run-1", "bg", unit, env, task)
	unit.done <- fmt.Errorf("collector failed")

	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: record.id}},
	}
	if err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), writer); err != nil {
		t.Fatalf("handle stop: %v", err)
	}

	var sawEvent, sawOutput, sawMetric, sawStop bool
	for out.Len() > 0 {
		frame := readServerTestFrame(t, &out)
		switch {
		case frame.GetBackgroundEvent() != nil:
			event := frame.GetBackgroundEvent()
			if event.GetTaskId() != record.id || event.GetEvent() != "fatal_error" {
				t.Fatalf("background event = %#v", event)
			}
			if event.GetError() == nil || event.GetError().GetMessage() != "collector failed" {
				t.Fatalf("background event error = %#v", event.GetError())
			}
			sawEvent = true
		case frame.GetSetOutput() != nil:
			if frame.GetRequestId() != "start-1" {
				t.Fatalf("output request id = %q, want start-1", frame.GetRequestId())
			}
			sawOutput = true
		case frame.GetMetricFlush() != nil:
			if frame.GetRequestId() != "start-1" {
				t.Fatalf("metric request id = %q, want start-1", frame.GetRequestId())
			}
			sawMetric = true
		case frame.GetStopResponse() != nil:
			if frame.GetRequestId() != "stop-1" {
				t.Fatalf("stop response request id = %q, want stop-1", frame.GetRequestId())
			}
			sawStop = true
		default:
			t.Fatalf("unexpected frame = %#v", frame)
		}
	}
	if !sawEvent || !sawOutput || !sawMetric || !sawStop {
		t.Fatalf("frames saw event=%v output=%v metric=%v stop=%v", sawEvent, sawOutput, sawMetric, sawStop)
	}
}

func testServerStopAfterBackgroundEventFlushesFinalState(t *testing.T, doneErr error, wantEvent string) {
	t.Helper()
	unit := &serverBackgroundUnit{
		done:            make(chan error, 1),
		writeFinalState: true,
	}
	srv := newServer(Plugin{Name: "server-bg", Version: "dev", Units: []contract.Unit{unit}})
	toHostReader, toHostWriter := io.Pipe()
	serverErr := make(chan error, 1)
	startFrame := &protocol.Frame{
		RequestId: "start-1",
		Body: &protocol.Frame_StartRequest{StartRequest: &protocol.StartRequest{
			UnitName: "bg",
			Kind:     "test.server_background/v1",
			RunId:    "run-1",
			SpecJson: []byte(`{}`),
		}},
	}
	go func() {
		err := srv.handleStart(context.Background(), startFrame, startFrame.GetStartRequest(), nil, protocol.NewFrameWriter(toHostWriter))
		serverErr <- err
	}()

	hostReader := protocol.NewFrameReader(toHostReader, 16<<20)
	taskID := readServerPipeFrame(t, hostReader).GetStartResponse().GetTaskId()
	if taskID == "" {
		t.Fatalf("empty task id")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("handle start: %v", err)
	}

	unit.done <- doneErr
	eventFrame := readServerPipeFrame(t, hostReader)
	event := eventFrame.GetBackgroundEvent()
	if event == nil {
		t.Fatalf("event frame = %#v", eventFrame)
	}
	if event.GetTaskId() != taskID || event.GetEvent() != wantEvent {
		t.Fatalf("event = %#v, want task %q event %q", event, taskID, wantEvent)
	}

	serverErr = make(chan error, 1)
	stopFrame := &protocol.Frame{
		RequestId: "stop-1",
		Body:      &protocol.Frame_StopRequest{StopRequest: &protocol.StopRequest{TaskId: taskID}},
	}
	go func() {
		err := srv.handleStop(context.Background(), stopFrame, stopFrame.GetStopRequest(), protocol.NewFrameWriter(toHostWriter))
		_ = toHostWriter.Close()
		serverErr <- err
	}()

	outputFrame := readServerPipeFrame(t, hostReader)
	if outputFrame.GetRequestId() != "start-1" {
		t.Fatalf("output request id = %q, want start-1", outputFrame.GetRequestId())
	}
	output := outputFrame.GetSetOutput()
	if output == nil || output.GetName() != "summary" {
		t.Fatalf("output frame = %#v", outputFrame)
	}
	metricFrame := readServerPipeFrame(t, hostReader)
	if metricFrame.GetRequestId() != "start-1" {
		t.Fatalf("metric request id = %q, want start-1", metricFrame.GetRequestId())
	}
	flush := metricFrame.GetMetricFlush()
	if flush == nil || len(flush.GetMetrics()) != 1 || flush.GetMetrics()[0].GetName() != "background_ticks" || flush.GetMetrics()[0].GetSum() != 3 {
		t.Fatalf("metric flush = %#v", flush)
	}
	stopResponse := readServerPipeFrame(t, hostReader)
	if stopResponse.GetRequestId() != "stop-1" || stopResponse.GetStopResponse() == nil {
		t.Fatalf("stop response = %#v", stopResponse)
	}
	if !unit.stopCalled {
		t.Fatalf("Stop was not called")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("handle stop: %v", err)
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

func readServerPipeFrame(t *testing.T, reader *protocol.FrameReader) *protocol.Frame {
	t.Helper()
	frame, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return frame
}

func readServerFrameFromChannel(t *testing.T, frames <-chan *protocol.Frame, errs <-chan error) *protocol.Frame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case err := <-errs:
		t.Fatalf("read frame: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for server frame")
	}
	return nil
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

type artifactRunUnit struct{}

func (artifactRunUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.artifact/v1",
		Outputs: []contract.PortDef{{
			Name: "result",
			Type: "port.demo.result/v1",
		}},
		Artifacts: []contract.ArtifactDef{{
			Name:        "metrics.jsonl",
			ContentType: "application/jsonl",
		}},
	}
}
func (artifactRunUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (artifactRunUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (artifactRunUnit) Run(ctx context.Context, env contract.RunEnv) error {
	artifact, err := env.OpenArtifact("metrics.jsonl")
	if err != nil {
		return err
	}
	if _, err := artifact.Write([]byte("one\n")); err != nil {
		return err
	}
	if _, err := artifact.Write([]byte("two\n")); err != nil {
		return err
	}
	if err := artifact.Close(); err != nil {
		return err
	}
	return env.SetOutput("result", map[string]any{"ok": true})
}

type typedInputRequest struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

type typedInputUnit struct{}

func (typedInputUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "demo.typed_input/v1",
		Inputs: []contract.PortDef{{
			Name: "request",
			Type: "port.demo.request/v1",
		}},
		Outputs: []contract.PortDef{{
			Name: "response",
			Type: "port.demo.response/v1",
		}},
	}
}
func (typedInputUnit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	return nil
}
func (typedInputUnit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (typedInputUnit) Run(ctx context.Context, env contract.RunEnv) error {
	request, err := contract.Input[typedInputRequest](env, "request")
	if err != nil {
		return err
	}
	return env.SetOutput("response", map[string]any{
		"summary": fmt.Sprintf("%s:%d", request.Message, request.Count),
	})
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

type serverBackgroundUnit struct {
	startCalled             bool
	runCalled               bool
	stopCalled              bool
	writeFinalState         bool
	writeArtifacts          bool
	returnNilTask           bool
	stopCalls               int
	stopErrorsRemaining     int
	badOutputStopsRemaining int
	badReportStopsRemaining int
	doneOnStop              bool
	doneOnStopErr           error
	done                    chan error
	doneClosed              bool
	env                     contract.RunEnv
}

func (u *serverBackgroundUnit) Definition() contract.Definition {
	return contract.Definition{
		Kind: "test.server_background/v1",
		Outputs: []contract.PortDef{{
			Name: "summary",
			Type: "port.test.summary/v1",
			Meta: contract.PortMeta{Reportable: true},
		}},
		Metrics: []contract.MetricDef{{
			Name: "background_ticks",
			Type: "counter",
		}},
		Artifacts: []contract.ArtifactDef{{
			Name:        "background.jsonl",
			ContentType: "application/jsonl",
		}},
	}
}

func (u *serverBackgroundUnit) Validate(context.Context, contract.ValidateEnv) error { return nil }
func (u *serverBackgroundUnit) Plan(context.Context, contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{}, nil
}
func (u *serverBackgroundUnit) Run(context.Context, contract.RunEnv) error {
	u.runCalled = true
	return nil
}
func (u *serverBackgroundUnit) Start(ctx context.Context, env contract.RunEnv) (contract.BackgroundTask, error) {
	u.startCalled = true
	u.env = env
	if u.done == nil {
		u.done = make(chan error)
	}
	if u.writeArtifacts {
		if err := writeServerBackgroundArtifact(env, "start\n"); err != nil {
			return nil, err
		}
	}
	if u.returnNilTask {
		return nil, nil
	}
	return serverBackgroundTask{unit: u}, nil
}

type serverBackgroundTask struct {
	unit *serverBackgroundUnit
}

func (t serverBackgroundTask) Stop(context.Context) error {
	t.unit.stopCalled = true
	t.unit.stopCalls++
	if t.unit.stopErrorsRemaining > 0 {
		t.unit.stopErrorsRemaining--
		return fmt.Errorf("stop failed")
	}
	if t.unit.doneOnStop {
		t.unit.done <- t.unit.doneOnStopErr
		defer t.unit.closeDone()
	}
	if t.unit.writeFinalState {
		if t.unit.badOutputStopsRemaining > 0 {
			t.unit.badOutputStopsRemaining--
			return t.unit.env.SetOutput("summary", make(chan int))
		}
		if t.unit.badReportStopsRemaining > 0 {
			t.unit.badReportStopsRemaining--
			return t.unit.env.SetOutput("summary", badServerReportValue{Visible: "visible"})
		}
		if err := t.unit.env.SetOutput("summary", map[string]any{"stopped": true}); err != nil {
			return err
		}
		t.unit.env.EmitCounter("background_ticks", 3, nil)
	}
	if t.unit.writeArtifacts {
		return writeServerBackgroundArtifact(t.unit.env, "stop\n")
	}
	return nil
}

func (t serverBackgroundTask) Done() <-chan error {
	return t.unit.done
}

func (u *serverBackgroundUnit) closeDone() {
	if u.doneClosed {
		return
	}
	u.doneClosed = true
	close(u.done)
}

type badServerReportValue struct {
	Visible string `json:"visible"`
}

func (v badServerReportValue) ReportOutput() any {
	return make(chan int)
}

func writeServerBackgroundArtifact(env contract.RunEnv, value string) error {
	artifact, err := env.OpenArtifact("background.jsonl")
	if err != nil {
		return err
	}
	if _, err := artifact.Write([]byte(value)); err != nil {
		_ = artifact.Close()
		return err
	}
	return artifact.Close()
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
