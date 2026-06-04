package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

type Plugin struct {
	Name    string
	Version string
	Units   []contract.Unit
}

type server struct {
	manifest    pluginhost.Plugin
	unitsByKind map[string]contract.Unit

	writeMu  sync.Mutex
	taskMu   sync.Mutex
	nextTask int64
	tasks    map[string]*backgroundTaskRecord
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

func newServer(plugin Plugin) *server {
	unitsByKind := make(map[string]contract.Unit, len(plugin.Units))
	for _, unit := range plugin.Units {
		unitsByKind[unit.Definition().Kind] = unit
	}
	return &server{
		manifest:    ManifestFromUnits(plugin.Name, plugin.Version, plugin.Units),
		unitsByKind: unitsByKind,
		tasks:       make(map[string]*backgroundTaskRecord),
	}
}

func (s *server) unit(kind string) (contract.Unit, error) {
	unit, ok := s.unitsByKind[kind]
	if !ok {
		return nil, fmt.Errorf("unit kind %q is not registered", kind)
	}
	return unit, nil
}

func Serve(plugin Plugin, stdin io.Reader, stdout io.Writer) error {
	srv := newServer(plugin)
	reader := protocol.NewFrameReader(stdin, 16<<20)
	writer := protocol.NewFrameWriter(stdout)
	ctx := context.Background()

	for {
		frame, err := reader.ReadFrame()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		switch frame.Body.(type) {
		case *protocol.Frame_HandshakeRequest:
			if err := srv.writeFrame(writer, &protocol.Frame{
				RequestId: frame.GetRequestId(),
				Body: &protocol.Frame_HandshakeResponse{HandshakeResponse: &protocol.HandshakeResponse{
					Manifest:         manifestToProto(srv.manifest),
					SelectedProtocol: "wkbench.plugin/v1",
				}},
			}); err != nil {
				return err
			}
		case *protocol.Frame_ListUnitsRequest:
			if err := srv.writeFrame(writer, &protocol.Frame{
				RequestId: frame.GetRequestId(),
				Body: &protocol.Frame_ListUnitsResponse{ListUnitsResponse: &protocol.ListUnitsResponse{
					Units: unitsToProto(srv.manifest.Units),
				}},
			}); err != nil {
				return err
			}
		case *protocol.Frame_ValidateRequest:
			if err := srv.handleValidate(ctx, frame, frame.GetValidateRequest(), writer); err != nil {
				return err
			}
		case *protocol.Frame_PlanRequest:
			if err := srv.handlePlan(ctx, frame, frame.GetPlanRequest(), writer); err != nil {
				return err
			}
		case *protocol.Frame_RunRequest:
			if err := srv.handleRun(ctx, frame, frame.GetRunRequest(), reader, writer); err != nil {
				return err
			}
		case *protocol.Frame_StartRequest:
			if err := srv.handleStart(ctx, frame, frame.GetStartRequest(), reader, writer); err != nil {
				return err
			}
		case *protocol.Frame_StopRequest:
			if err := srv.handleStop(ctx, frame, frame.GetStopRequest(), writer); err != nil {
				return err
			}
		default:
			if err := srv.writeProtocolError(writer, frame.GetRequestId(), "UNSUPPORTED", "unsupported frame"); err != nil {
				return err
			}
		}
	}
}

func (s *server) writeFrame(writer *protocol.FrameWriter, frame *protocol.Frame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writer.WriteFrame(frame)
}

func (s *server) handleValidate(ctx context.Context, frame *protocol.Frame, req *protocol.ValidateRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.GetKind())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.GetSpecJson())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	env := contract.NewTestRunEnv("", req.GetUnitName(), nil, spec)
	if err := unit.Validate(ctx, env); err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", err.Error())
	}
	return s.writeFrame(writer, &protocol.Frame{
		RequestId: frame.GetRequestId(),
		Body:      &protocol.Frame_ValidateResponse{ValidateResponse: &protocol.ValidateResponse{}},
	})
}

func (s *server) handlePlan(ctx context.Context, frame *protocol.Frame, req *protocol.PlanRequest, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.GetKind())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.GetSpecJson())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	env := contract.NewTestRunEnv(req.GetRunId(), req.GetUnitName(), nil, spec)
	env.SetRunDuration(time.Duration(req.GetRunDurationMillis()) * time.Millisecond)
	if req.GetWorkerCount() > 0 {
		env.SetWorkerCount(int(req.GetWorkerCount()))
	}
	plan, err := unit.Plan(ctx, env)
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	payload, err := encodeJSONPayload(plan)
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "PLAN_ERROR", err.Error())
	}
	return s.writeFrame(writer, &protocol.Frame{
		RequestId: frame.GetRequestId(),
		Body:      &protocol.Frame_PlanResponse{PlanResponse: &protocol.PlanResponse{PlanJson: payload}},
	})
}

func (s *server) handleRun(ctx context.Context, frame *protocol.Frame, req *protocol.RunRequest, reader *protocol.FrameReader, writer *protocol.FrameWriter) error {
	unit, err := s.unit(req.GetKind())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	spec, err := decodeSpecMap(req.GetSpecJson())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	inputs, err := decodeInputValues(req.GetInputs())
	if err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	env := newRemoteRunEnv(frame.GetRequestId(), req, inputs, spec, unit.Definition().Artifacts, reader, func(out *protocol.Frame) error {
		return s.writeFrame(writer, out)
	})
	configureRunEnv(env, req.GetRunDurationMillis(), req.GetWorkerCount())
	if err := unit.Run(ctx, env); err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	if err := s.writeOutputs(frame.GetRequestId(), env, unit, writer); err != nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	if err := s.writeMetricFlush(frame.GetRequestId(), env, writer); err != nil {
		return err
	}
	return s.writeFrame(writer, &protocol.Frame{
		RequestId: frame.GetRequestId(),
		Body:      &protocol.Frame_TerminalStatus{TerminalStatus: &protocol.TerminalStatus{Ok: true}},
	})
}

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
	if task == nil {
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", "background unit returned nil task")
	}
	record := s.storeBackgroundTask(frame.GetRequestId(), req.GetRunId(), req.GetUnitName(), unit, env, task)
	if err := s.writeFrame(writer, &protocol.Frame{
		RequestId:      frame.GetRequestId(),
		RunId:          req.GetRunId(),
		UnitInstanceId: req.GetUnitName(),
		Body:           &protocol.Frame_StartResponse{StartResponse: &protocol.StartResponse{TaskId: record.id}},
	}); err != nil {
		return err
	}
	go s.monitorBackgroundTask(record.id, task, writer)
	return nil
}

func (s *server) handleStop(ctx context.Context, frame *protocol.Frame, req *protocol.StopRequest, writer *protocol.FrameWriter) error {
	record, ok := s.beginBackgroundTaskStop(req.GetTaskId())
	if !ok {
		return s.writeProtocolError(writer, frame.GetRequestId(), "CONFIG_ERROR", fmt.Sprintf("background task %q is not active", req.GetTaskId()))
	}
	if err := record.task.Stop(ctx); err != nil {
		s.abortBackgroundTaskStop(record.id, record)
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	if err := s.writeOutputs(record.requestID, record.env, record.unit, writer); err != nil {
		s.abortBackgroundTaskStop(record.id, record)
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	if err := s.writeMetricFlush(record.requestID, record.env, writer); err != nil {
		s.abortBackgroundTaskStop(record.id, record)
		return s.writeProtocolError(writer, frame.GetRequestId(), "RUN_ERROR", err.Error())
	}
	if err := s.writeFrame(writer, &protocol.Frame{
		RequestId:      frame.GetRequestId(),
		RunId:          record.runID,
		UnitInstanceId: record.unitName,
		Body:           &protocol.Frame_StopResponse{StopResponse: &protocol.StopResponse{}},
	}); err != nil {
		s.abortBackgroundTaskStop(record.id, record)
		return err
	}
	s.completeBackgroundTaskStop(record.id, record)
	return nil
}

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

func (s *server) beginBackgroundTaskStop(taskID string) (*backgroundTaskRecord, bool) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	record, ok := s.tasks[taskID]
	if !ok {
		return nil, false
	}
	record.stopping = true
	return record, true
}

func (s *server) abortBackgroundTaskStop(taskID string, record *backgroundTaskRecord) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.tasks[taskID] == record {
		record.stopping = false
	}
}

func (s *server) completeBackgroundTaskStop(taskID string, record *backgroundTaskRecord) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.tasks[taskID] == record {
		delete(s.tasks, taskID)
	}
}

func (s *server) monitorBackgroundTask(taskID string, task contract.BackgroundTask, writer *protocol.FrameWriter) {
	err, ok := <-task.Done()
	if !ok {
		err = nil
	}
	s.taskMu.Lock()
	record, active := s.tasks[taskID]
	stopping := false
	if active {
		stopping = record.stopping
	}
	s.taskMu.Unlock()
	if !active || stopping {
		return
	}
	event := "completed"
	var rpcErr *protocol.Error
	if err != nil {
		event = "fatal_error"
		rpcErr = &protocol.Error{Code: "BACKGROUND_ERROR", Message: err.Error()}
	}
	_ = s.writeFrame(writer, &protocol.Frame{
		RequestId:      record.requestID,
		RunId:          record.runID,
		UnitInstanceId: record.unitName,
		Body: &protocol.Frame_BackgroundEvent{BackgroundEvent: &protocol.BackgroundEvent{
			TaskId: taskID,
			Event:  event,
			Error:  rpcErr,
		}},
	})
}

type metricSnapshotReader interface {
	MetricSnapshots() []contract.MetricSnapshot
}

func (s *server) writeMetricFlush(requestID string, env metricSnapshotReader, writer *protocol.FrameWriter) error {
	snapshots := env.MetricSnapshots()
	if len(snapshots) == 0 {
		return nil
	}
	metrics := make([]*protocol.MetricSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		metrics = append(metrics, &protocol.MetricSnapshot{
			Name:   snapshot.Name,
			Type:   snapshot.Type,
			Labels: map[string]string(snapshot.Labels),
			Count:  snapshot.Count,
			Sum:    snapshot.Sum,
			Min:    snapshot.Min,
			Max:    snapshot.Max,
		})
	}
	return s.writeFrame(writer, &protocol.Frame{
		RequestId: requestID,
		Body:      &protocol.Frame_MetricFlush{MetricFlush: &protocol.MetricFlush{Metrics: metrics}},
	})
}

func (s *server) writeOutputs(requestID string, env *remoteRunEnv, unit contract.Unit, writer *protocol.FrameWriter) error {
	for _, output := range unit.Definition().Outputs {
		value, ok := env.Output(output.Name)
		if !ok {
			continue
		}
		payload, err := encodeJSONPayload(value)
		if err != nil {
			return err
		}
		var reportPayload []byte
		if output.Meta.Reportable && !output.Meta.Sensitive {
			reportValue := value
			if reportable, ok := value.(contract.ReportableOutput); ok {
				reportValue = reportable.ReportOutput()
			}
			reportPayload, err = encodeJSONPayload(reportValue)
			if err != nil {
				return err
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

const artifactChunkMaxBytes = 64 << 10

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

func (e *remoteRunEnv) OpenArtifact(name string) (io.WriteCloser, error) {
	if _, ok := e.artifactDefs[name]; !ok {
		return nil, fmt.Errorf("artifact %q not declared", name)
	}
	if e.reader == nil {
		return nil, fmt.Errorf("artifact %q cannot be opened without a plugin response reader", name)
	}
	e.ioMu.Lock()
	defer e.ioMu.Unlock()
	if err := e.writeFrame(&protocol.Frame{
		RequestId:      e.requestID,
		RunId:          e.runID,
		UnitInstanceId: e.unitName,
		Body:           &protocol.Frame_ArtifactOpen{ArtifactOpen: &protocol.ArtifactOpen{Name: name}},
	}); err != nil {
		return nil, fmt.Errorf("write artifact open %q: %w", name, err)
	}
	frame, err := e.reader.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read artifact opened %q: %w", name, err)
	}
	if err := validateResponseFrameID(frame, e.requestID); err != nil {
		return nil, err
	}
	if rpcErr := frame.GetError(); rpcErr != nil {
		return nil, remoteProtocolError(rpcErr)
	}
	opened := frame.GetArtifactOpened()
	if opened == nil {
		return nil, fmt.Errorf("expected artifact opened response for %q", name)
	}
	if opened.GetHandle() == "" {
		return nil, fmt.Errorf("artifact opened response for %q missing handle", name)
	}
	return &remoteArtifactWriter{env: e, name: name, handle: opened.GetHandle()}, nil
}

type remoteArtifactWriter struct {
	env      *remoteRunEnv
	name     string
	handle   string
	sequence int64
	closed   bool
}

func (w *remoteArtifactWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("artifact %q is closed", w.name)
	}
	if len(p) == 0 {
		return 0, nil
	}
	written := 0
	for written < len(p) {
		end := written + artifactChunkMaxBytes
		if end > len(p) {
			end = len(p)
		}
		w.sequence++
		chunk := append([]byte(nil), p[written:end]...)
		if err := w.env.writeArtifactChunk(w.handle, w.sequence, chunk); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}

func (w *remoteArtifactWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.env.closeArtifact(w.handle, w.name)
}

func (e *remoteRunEnv) writeArtifactChunk(handle string, sequence int64, data []byte) error {
	e.ioMu.Lock()
	defer e.ioMu.Unlock()
	if err := e.writeFrame(&protocol.Frame{
		RequestId:      e.requestID,
		RunId:          e.runID,
		UnitInstanceId: e.unitName,
		Body: &protocol.Frame_ArtifactChunk{ArtifactChunk: &protocol.ArtifactChunk{
			Handle:   handle,
			Sequence: sequence,
			Data:     data,
		}},
	}); err != nil {
		return fmt.Errorf("write artifact chunk: %w", err)
	}
	return nil
}

func (e *remoteRunEnv) closeArtifact(handle, name string) error {
	e.ioMu.Lock()
	defer e.ioMu.Unlock()
	if err := e.writeFrame(&protocol.Frame{
		RequestId:      e.requestID,
		RunId:          e.runID,
		UnitInstanceId: e.unitName,
		Body:           &protocol.Frame_ArtifactClose{ArtifactClose: &protocol.ArtifactClose{Handle: handle}},
	}); err != nil {
		return fmt.Errorf("write artifact close %q: %w", name, err)
	}
	frame, err := e.reader.ReadFrame()
	if err != nil {
		return fmt.Errorf("read artifact closed %q: %w", name, err)
	}
	if err := validateResponseFrameID(frame, e.requestID); err != nil {
		return err
	}
	if rpcErr := frame.GetError(); rpcErr != nil {
		return remoteProtocolError(rpcErr)
	}
	closed := frame.GetArtifactClosed()
	if closed == nil {
		return fmt.Errorf("expected artifact closed response for %q", name)
	}
	if closed.GetHandle() != handle {
		return fmt.Errorf("artifact closed handle = %q, want %q", closed.GetHandle(), handle)
	}
	return nil
}

func validateResponseFrameID(frame *protocol.Frame, requestID string) error {
	if frame.GetRequestId() != requestID {
		return fmt.Errorf("response request id = %q, want %q", frame.GetRequestId(), requestID)
	}
	return nil
}

func remoteProtocolError(err *protocol.Error) error {
	if err == nil {
		return fmt.Errorf("plugin protocol error")
	}
	return fmt.Errorf("plugin error %s: %s", err.GetCode(), err.GetMessage())
}

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

func encodeJSONPayload(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode json payload: %w", err)
	}
	return payload, nil
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

func ManifestFromUnits(name, version string, units []contract.Unit) pluginhost.Plugin {
	out := pluginhost.Plugin{Name: name, Version: version, Protocol: "wkbench.plugin/v1"}
	for _, unit := range units {
		def := unit.Definition()
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
	}
	return out
}

func clonePortDefs(ports []contract.PortDef) []contract.PortDef {
	ports = slices.Clone(ports)
	for i := range ports {
		ports[i].Meta.Encodings = slices.Clone(ports[i].Meta.Encodings)
		ports[i].Meta.Operations = slices.Clone(ports[i].Meta.Operations)
	}
	return ports
}

func manifestToProto(manifest pluginhost.Plugin) *protocol.PluginManifest {
	return &protocol.PluginManifest{
		Name:     manifest.Name,
		Version:  manifest.Version,
		Protocol: manifest.Protocol,
		Source:   manifest.Source,
		Checksum: manifest.Checksum,
		Units:    unitsToProto(manifest.Units),
	}
}

func unitsToProto(units []pluginhost.Unit) []*protocol.UnitDefinition {
	out := make([]*protocol.UnitDefinition, 0, len(units))
	for _, unit := range units {
		out = append(out, unitToProto(unit))
	}
	return out
}

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

func portsToProto(ports []contract.PortDef) []*protocol.PortDef {
	out := make([]*protocol.PortDef, 0, len(ports))
	for _, port := range ports {
		out = append(out, portToProto(port))
	}
	return out
}

func portToProto(port contract.PortDef) *protocol.PortDef {
	return &protocol.PortDef{
		Name:     port.Name,
		Type:     string(port.Type),
		Optional: port.Optional,
		Meta: &protocol.PortMeta{
			Boundary:        string(port.Meta.Boundary),
			Transport:       string(port.Meta.Transport),
			Schema:          port.Meta.Schema,
			Encodings:       slices.Clone(port.Meta.Encodings),
			MaxPayloadBytes: port.Meta.MaxPayloadBytes,
			Sensitive:       port.Meta.Sensitive,
			Reportable:      port.Meta.Reportable,
			Operations:      slices.Clone(port.Meta.Operations),
		},
	}
}

func metricsToProto(metrics []contract.MetricDef) []*protocol.MetricDef {
	out := make([]*protocol.MetricDef, 0, len(metrics))
	for _, metric := range metrics {
		out = append(out, &protocol.MetricDef{Name: metric.Name, Type: metric.Type})
	}
	return out
}

func artifactsToProto(artifacts []contract.ArtifactDef) []*protocol.ArtifactDef {
	out := make([]*protocol.ArtifactDef, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, &protocol.ArtifactDef{Name: artifact.Name, ContentType: artifact.ContentType})
	}
	return out
}
