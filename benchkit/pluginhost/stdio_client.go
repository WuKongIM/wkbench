package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/protocol"
)

type StdioClient struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	reader *protocol.FrameReader
	writer *protocol.FrameWriter

	ioMu      sync.Mutex
	closeOnce sync.Once
	closeErr  error
	waitOnce  sync.Once
	waitCh    chan error
	stateMu   sync.Mutex
	closed    bool
	killed    bool
	nextSeq   int64
}

var errStdioClientClosed = errors.New("stdio plugin client closed")

type readResult struct {
	frame *protocol.Frame
	err   error
}

func StartStdioClient(ctx context.Context, path string) (*StdioClient, error) {
	cmd := exec.CommandContext(ctx, path)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open plugin stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open plugin stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin: %w", err)
	}

	client := &StdioClient{
		cmd:    cmd,
		stdin:  stdin,
		reader: protocol.NewFrameReader(stdout, 16<<20),
		writer: protocol.NewFrameWriter(stdin),
		waitCh: make(chan error, 1),
	}
	return client, nil
}

func (c *StdioClient) Handshake(ctx context.Context) (Plugin, error) {
	const requestID = "handshake"
	if err := ctx.Err(); err != nil {
		c.shutdown(true)
		c.startWait()
		return Plugin{}, err
	}

	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	if c.isClosed() {
		return Plugin{}, errStdioClientClosed
	}

	if err := c.writer.WriteFrame(&protocol.Frame{
		RequestId: requestID,
		Body: &protocol.Frame_HandshakeRequest{HandshakeRequest: &protocol.HandshakeRequest{
			HostProtocol: "wkbench.plugin/v1",
			MinProtocol:  "wkbench.plugin/v1",
			MaxProtocol:  "wkbench.plugin/v1",
		}},
	}); err != nil {
		return Plugin{}, fmt.Errorf("write handshake request: %w", err)
	}

	readResultCh := make(chan readResult, 1)
	go func() {
		frame, err := c.reader.ReadFrame()
		readResultCh <- readResult{frame: frame, err: err}
	}()

	result, canceled, ctxErr := waitForFrame(ctx, readResultCh)
	if canceled {
		c.shutdown(true)
		c.startWait()
		<-readResultCh
		return Plugin{}, fmt.Errorf("handshake canceled: %w", ctxErr)
	}
	frame, err := result.frame, result.err
	if err != nil {
		return Plugin{}, fmt.Errorf("read handshake response: %w", err)
	}
	if frame.GetRequestId() != requestID {
		return Plugin{}, fmt.Errorf("handshake response request id = %q, want %q", frame.GetRequestId(), requestID)
	}
	if rpcErr := frame.GetError(); rpcErr != nil {
		return Plugin{}, fmt.Errorf("plugin error %s: %s", rpcErr.GetCode(), rpcErr.GetMessage())
	}
	response := frame.GetHandshakeResponse()
	if response == nil {
		return Plugin{}, fmt.Errorf("expected handshake response frame")
	}
	manifest := pluginFromProto(response.GetManifest())
	if response.GetSelectedProtocol() != "" {
		manifest.Protocol = response.GetSelectedProtocol()
	}
	return manifest, nil
}

func (c *StdioClient) Validate(ctx context.Context, req UnitRequest) error {
	if err := ctx.Err(); err != nil {
		c.shutdown(true)
		c.startWait()
		return err
	}

	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	if c.isClosed() {
		return errStdioClientClosed
	}

	requestID := c.nextRequestID("validate")
	frame := &protocol.Frame{
		RequestId:      requestID,
		RunId:          req.RunID,
		UnitInstanceId: req.UnitName,
		Body: &protocol.Frame_ValidateRequest{ValidateRequest: &protocol.ValidateRequest{
			UnitName: req.UnitName,
			Kind:     req.Kind,
			SpecJson: req.SpecJSON,
		}},
	}
	response, err := c.writeRequestAndReadFrame(ctx, frame, "validate")
	if err != nil {
		return err
	}
	if rpcErr := response.GetError(); rpcErr != nil {
		return pluginRPCError(rpcErr)
	}
	if response.GetValidateResponse() == nil {
		return fmt.Errorf("expected validate response frame")
	}
	return nil
}

func (c *StdioClient) Plan(ctx context.Context, req UnitRequest) (contract.Plan, error) {
	if err := ctx.Err(); err != nil {
		c.shutdown(true)
		c.startWait()
		return contract.Plan{}, err
	}

	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	if c.isClosed() {
		return contract.Plan{}, errStdioClientClosed
	}

	requestID := c.nextRequestID("plan")
	frame := &protocol.Frame{
		RequestId:      requestID,
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
	}
	response, err := c.writeRequestAndReadFrame(ctx, frame, "plan")
	if err != nil {
		return contract.Plan{}, err
	}
	if rpcErr := response.GetError(); rpcErr != nil {
		return contract.Plan{}, pluginRPCError(rpcErr)
	}
	planResponse := response.GetPlanResponse()
	if planResponse == nil {
		return contract.Plan{}, fmt.Errorf("expected plan response frame")
	}
	var plan contract.Plan
	if len(planResponse.GetPlanJson()) > 0 {
		if err := json.Unmarshal(planResponse.GetPlanJson(), &plan); err != nil {
			return contract.Plan{}, fmt.Errorf("decode plan json: %w", err)
		}
	}
	return plan, nil
}

func (c *StdioClient) Run(ctx context.Context, req RunRequest, env contract.RunEnv) error {
	if err := ctx.Err(); err != nil {
		c.shutdown(true)
		c.startWait()
		return err
	}

	inputs, err := encodeInputPortValues(req.InputDefs, req.InputSourceDefs, req.Inputs)
	if err != nil {
		return err
	}

	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	if c.isClosed() {
		return errStdioClientClosed
	}

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
			Inputs:            inputs,
		}},
	}); err != nil {
		return fmt.Errorf("write run request: %w", err)
	}

	artifacts := newRunArtifactState(env)
	defer artifacts.closeAll()

	for {
		frame, err := c.readFrame(ctx, "run")
		if err != nil {
			return err
		}
		if frame.GetRequestId() != requestID {
			return fmt.Errorf("run response request id = %q, want %q", frame.GetRequestId(), requestID)
		}
		switch body := frame.Body.(type) {
		case *protocol.Frame_SetOutput:
			if err := setOutputFromFrame(env, body.SetOutput); err != nil {
				return err
			}
		case *protocol.Frame_MetricFlush:
			applyMetricFlush(env, body.MetricFlush)
		case *protocol.Frame_ArtifactOpen:
			opened, err := artifacts.open(body.ArtifactOpen)
			if err != nil {
				return writeRunArtifactError(c.writer, requestID, req.RunID, req.UnitName, err)
			}
			if err := c.writer.WriteFrame(&protocol.Frame{
				RequestId:      requestID,
				RunId:          req.RunID,
				UnitInstanceId: req.UnitName,
				Body:           &protocol.Frame_ArtifactOpened{ArtifactOpened: opened},
			}); err != nil {
				return fmt.Errorf("write artifact opened response: %w", err)
			}
		case *protocol.Frame_ArtifactChunk:
			if err := artifacts.write(body.ArtifactChunk); err != nil {
				return writeRunArtifactError(c.writer, requestID, req.RunID, req.UnitName, err)
			}
		case *protocol.Frame_ArtifactClose:
			closed, err := artifacts.close(body.ArtifactClose)
			if err != nil {
				return writeRunArtifactError(c.writer, requestID, req.RunID, req.UnitName, err)
			}
			if err := c.writer.WriteFrame(&protocol.Frame{
				RequestId:      requestID,
				RunId:          req.RunID,
				UnitInstanceId: req.UnitName,
				Body:           &protocol.Frame_ArtifactClosed{ArtifactClosed: closed},
			}); err != nil {
				return fmt.Errorf("write artifact closed response: %w", err)
			}
		case *protocol.Frame_TerminalStatus:
			if body.TerminalStatus.GetOk() {
				return nil
			}
			if rpcErr := body.TerminalStatus.GetError(); rpcErr != nil {
				return pluginRPCError(rpcErr)
			}
			return fmt.Errorf("plugin run failed")
		case *protocol.Frame_Error:
			return pluginRPCError(body.Error)
		default:
			return fmt.Errorf("unexpected run response frame %T", frame.Body)
		}
	}
}

func writeRunArtifactError(writer *protocol.FrameWriter, requestID, runID, unitName string, err error) error {
	if writeErr := writer.WriteFrame(&protocol.Frame{
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

func (c *StdioClient) nextRequestID(prefix string) string {
	c.nextSeq++
	return fmt.Sprintf("%s-%d", prefix, c.nextSeq)
}

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

func newRunArtifactState(env contract.RunEnv) *runArtifactState {
	return &runArtifactState{
		env:     env,
		writers: make(map[string]*hostArtifactWriter),
	}
}

func (s *runArtifactState) open(open *protocol.ArtifactOpen) (*protocol.ArtifactOpened, error) {
	if open == nil {
		return nil, fmt.Errorf("artifact open frame missing body")
	}
	writer, err := s.env.OpenArtifact(open.GetName())
	if err != nil {
		return nil, fmt.Errorf("open artifact %q: %w", open.GetName(), err)
	}
	s.next++
	handle := fmt.Sprintf("artifact-%d", s.next)
	s.writers[handle] = &hostArtifactWriter{name: open.GetName(), writer: writer}
	return &protocol.ArtifactOpened{Name: open.GetName(), Handle: handle}, nil
}

func (s *runArtifactState) write(chunk *protocol.ArtifactChunk) error {
	if chunk == nil {
		return fmt.Errorf("artifact chunk frame missing body")
	}
	writer, ok := s.writers[chunk.GetHandle()]
	if !ok {
		return fmt.Errorf("unknown artifact handle %q", chunk.GetHandle())
	}
	n, err := writer.writer.Write(chunk.GetData())
	writer.size += int64(n)
	if err != nil {
		return fmt.Errorf("write artifact %q: %w", writer.name, err)
	}
	if n != len(chunk.GetData()) {
		return fmt.Errorf("write artifact %q: short write %d of %d", writer.name, n, len(chunk.GetData()))
	}
	return nil
}

func (s *runArtifactState) close(closeFrame *protocol.ArtifactClose) (*protocol.ArtifactClosed, error) {
	if closeFrame == nil {
		return nil, fmt.Errorf("artifact close frame missing body")
	}
	handle := closeFrame.GetHandle()
	writer, ok := s.writers[handle]
	if !ok {
		return nil, fmt.Errorf("unknown artifact handle %q", handle)
	}
	delete(s.writers, handle)
	if err := writer.writer.Close(); err != nil {
		return nil, fmt.Errorf("close artifact %q: %w", writer.name, err)
	}
	return &protocol.ArtifactClosed{Handle: handle, SizeBytes: writer.size}, nil
}

func (s *runArtifactState) closeAll() {
	for handle, writer := range s.writers {
		_ = writer.writer.Close()
		delete(s.writers, handle)
	}
}

func (c *StdioClient) writeRequestAndReadFrame(ctx context.Context, frame *protocol.Frame, op string) (*protocol.Frame, error) {
	if err := c.writer.WriteFrame(frame); err != nil {
		return nil, fmt.Errorf("write %s request: %w", op, err)
	}
	response, err := c.readFrame(ctx, op)
	if err != nil {
		return nil, err
	}
	if response.GetRequestId() != frame.GetRequestId() {
		return nil, fmt.Errorf("%s response request id = %q, want %q", op, response.GetRequestId(), frame.GetRequestId())
	}
	return response, nil
}

func (c *StdioClient) readFrame(ctx context.Context, op string) (*protocol.Frame, error) {
	readResultCh := make(chan readResult, 1)
	go func() {
		frame, err := c.reader.ReadFrame()
		readResultCh <- readResult{frame: frame, err: err}
	}()

	result, canceled, ctxErr := waitForFrame(ctx, readResultCh)
	if canceled {
		c.shutdown(true)
		c.startWait()
		<-readResultCh
		return nil, fmt.Errorf("%s canceled: %w", op, ctxErr)
	}
	if result.err != nil {
		return nil, fmt.Errorf("read %s response: %w", op, result.err)
	}
	return result.frame, nil
}

func encodeInputPortValues(defs []contract.PortDef, sourceDefs map[string]contract.PortDef, inputs map[string]any) (map[string]*protocol.PortValue, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	defByName := make(map[string]contract.PortDef, len(defs))
	for _, def := range defs {
		defByName[def.Name] = def
	}
	out := make(map[string]*protocol.PortValue, len(inputs))
	for name, value := range inputs {
		def, ok := defByName[name]
		if !ok {
			return nil, fmt.Errorf("input %q has no remote port definition", name)
		}
		meta := def.Metadata()
		if err := validatePhase1InputPort("consumer input", def.Name, meta); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode input %q json: %w", name, err)
		}
		if int64(len(payload)) > meta.MaxPayloadBytes {
			return nil, fmt.Errorf("input %q json payload size %d exceeds max payload bytes %d", name, len(payload), meta.MaxPayloadBytes)
		}
		if sourceDef, ok := sourceDefs[name]; ok {
			if sourceDef.Type != def.Type {
				return nil, fmt.Errorf("producer output %q type %s does not match consumer input %q type %s", sourceDef.Name, sourceDef.Type, def.Name, def.Type)
			}
			sourceMeta := sourceDef.Metadata()
			if err := validatePhase1InputPort("producer output", sourceDef.Name, sourceMeta); err != nil {
				return nil, err
			}
			if int64(len(payload)) > sourceMeta.MaxPayloadBytes {
				return nil, fmt.Errorf("input %q json payload size %d exceeds producer output %q max payload bytes %d", name, len(payload), sourceDef.Name, sourceMeta.MaxPayloadBytes)
			}
		}
		out[name] = &protocol.PortValue{
			Type:      string(def.Type),
			Encoding:  "json",
			Transport: string(meta.Transport),
			Sensitive: meta.Sensitive,
			Payload:   payload,
		}
	}
	return out, nil
}

func validatePhase1InputPort(role, name string, meta contract.PortMeta) error {
	if meta.Boundary != contract.PortBoundaryData {
		return fmt.Errorf("%s %q boundary %s cannot cross the plugin RPC boundary in phase 1", role, name, meta.Boundary)
	}
	if meta.Transport != contract.PortTransportInline {
		return fmt.Errorf("%s %q transport %s is not supported for plugin RPC inputs in phase 1", role, name, meta.Transport)
	}
	if meta.Sensitive {
		return fmt.Errorf("%s %q is sensitive and cannot be sent inline over plugin RPC in phase 1", role, name)
	}
	if len(meta.Encodings) > 0 && !slices.Contains(meta.Encodings, "json") {
		return fmt.Errorf("%s %q must allow json encoding for plugin RPC inputs in phase 1", role, name)
	}
	return nil
}

func setOutputFromFrame(env contract.RunEnv, output *protocol.SetOutput) error {
	if output == nil {
		return fmt.Errorf("set output frame missing output")
	}
	var decoded any
	value := output.GetValue()
	if value != nil && len(value.GetPayload()) > 0 {
		if err := json.Unmarshal(value.GetPayload(), &decoded); err != nil {
			return fmt.Errorf("decode output %q json: %w", output.GetName(), err)
		}
	}
	stored := decoded
	if value.GetReportable() && !value.GetSensitive() {
		reportValue := decoded
		if len(value.GetReportPayload()) > 0 {
			if err := json.Unmarshal(value.GetReportPayload(), &reportValue); err != nil {
				return fmt.Errorf("decode output %q report json: %w", output.GetName(), err)
			}
		}
		stored = remoteReportableOutput{value: decoded, reportValue: reportValue}
	}
	if err := env.SetOutput(output.GetName(), stored); err != nil {
		return fmt.Errorf("set output %q: %w", output.GetName(), err)
	}
	return nil
}

type remoteReportableOutput struct {
	value       any
	reportValue any
}

func (o remoteReportableOutput) ReportOutput() any {
	return o.reportValue
}

func (o remoteReportableOutput) OutputValue() any {
	return o.value
}

func (o remoteReportableOutput) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.value)
}

func applyMetricFlush(env contract.RunEnv, flush *protocol.MetricFlush) {
	if flush == nil {
		return
	}
	for _, metric := range flush.GetMetrics() {
		if metric == nil || metric.GetCount() <= 0 {
			continue
		}
		snapshot := contract.MetricSnapshot{
			Name:   metric.GetName(),
			Type:   metric.GetType(),
			Labels: contract.Labels(metric.GetLabels()),
			Count:  metric.GetCount(),
			Sum:    metric.GetSum(),
			Min:    metric.GetMin(),
			Max:    metric.GetMax(),
		}
		if recorder, ok := env.(contract.MetricSnapshotRecorder); ok {
			recorder.RecordMetricSnapshot(snapshot)
			continue
		}
		if snapshot.Type != "duration" {
			replayCounterSnapshot(env, snapshot)
		}
	}
}

func replayCounterSnapshot(env contract.RunEnv, snapshot contract.MetricSnapshot) {
	count := snapshot.Count
	delta := snapshot.Sum / float64(count)
	for i := int64(0); i < count; i++ {
		env.EmitCounter(snapshot.Name, delta, snapshot.Labels)
	}
}

func pluginRPCError(err *protocol.Error) error {
	if err == nil {
		return fmt.Errorf("plugin error")
	}
	return fmt.Errorf("plugin error %s: %s", err.GetCode(), err.GetMessage())
}

func waitForFrame(ctx context.Context, readResultCh <-chan readResult) (readResult, bool, error) {
	select {
	case result := <-readResultCh:
		return result, false, nil
	default:
	}

	select {
	case result := <-readResultCh:
		return result, false, nil
	case <-ctx.Done():
		select {
		case result := <-readResultCh:
			return result, false, nil
		default:
			return readResult{}, true, ctx.Err()
		}
	}
}

func (c *StdioClient) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.close()
	})
	return c.closeErr
}

func (c *StdioClient) close() error {
	c.shutdown(false)
	done := c.startWait()

	select {
	case err := <-done:
		if err != nil && !c.wasKilled() {
			return fmt.Errorf("wait plugin: %w", err)
		}
		return nil
	case <-time.After(2 * time.Second):
		c.shutdown(true)
		err := <-done
		if err != nil && !c.wasKilled() {
			return fmt.Errorf("plugin did not exit after stdin close: %w", err)
		}
		return nil
	}
}

func (c *StdioClient) shutdown(kill bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if kill && !c.killed && c.cmd.Process != nil {
		if err := c.cmd.Process.Kill(); err == nil {
			c.killed = true
		}
	}
	if !c.closed {
		_ = c.stdin.Close()
		c.closed = true
	}
}

func (c *StdioClient) startWait() <-chan error {
	c.waitOnce.Do(func() {
		go func() {
			c.waitCh <- c.cmd.Wait()
		}()
	})
	return c.waitCh
}

func (c *StdioClient) isClosed() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.closed
}

func (c *StdioClient) wasKilled() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.killed
}

func pluginFromProto(manifest *protocol.PluginManifest) Plugin {
	if manifest == nil {
		return Plugin{}
	}
	return Plugin{
		Name:     manifest.GetName(),
		Version:  manifest.GetVersion(),
		Protocol: manifest.GetProtocol(),
		Source:   manifest.GetSource(),
		Checksum: manifest.GetChecksum(),
		Units:    unitsFromProto(manifest.GetUnits(), manifest.GetName()),
	}
}

func unitsFromProto(units []*protocol.UnitDefinition, pluginName string) []Unit {
	out := make([]Unit, 0, len(units))
	for _, unit := range units {
		out = append(out, unitFromProto(unit, pluginName))
	}
	return out
}

func unitFromProto(unit *protocol.UnitDefinition, pluginName string) Unit {
	if unit == nil {
		return Unit{PluginName: pluginName}
	}
	return Unit{
		PluginName:  pluginName,
		Kind:        unit.GetKind(),
		Title:       unit.GetTitle(),
		Description: unit.GetDescription(),
		Inputs:      portsFromProto(unit.GetInputs()),
		Outputs:     portsFromProto(unit.GetOutputs()),
		Metrics:     metricsFromProto(unit.GetMetrics()),
		Artifacts:   artifactsFromProto(unit.GetArtifacts()),
	}
}

func portsFromProto(ports []*protocol.PortDef) []contract.PortDef {
	out := make([]contract.PortDef, 0, len(ports))
	for _, port := range ports {
		out = append(out, portFromProto(port))
	}
	return out
}

func portFromProto(port *protocol.PortDef) contract.PortDef {
	if port == nil {
		return contract.PortDef{}
	}
	meta := port.GetMeta()
	return contract.PortDef{
		Name:     port.GetName(),
		Type:     contract.PortType(port.GetType()),
		Optional: port.GetOptional(),
		Meta: contract.PortMeta{
			Boundary:        contract.PortBoundary(meta.GetBoundary()),
			Transport:       contract.PortTransport(meta.GetTransport()),
			Schema:          meta.GetSchema(),
			Encodings:       slices.Clone(meta.GetEncodings()),
			MaxPayloadBytes: meta.GetMaxPayloadBytes(),
			Sensitive:       meta.GetSensitive(),
			Reportable:      meta.GetReportable(),
			Operations:      slices.Clone(meta.GetOperations()),
		},
	}
}

func metricsFromProto(metrics []*protocol.MetricDef) []contract.MetricDef {
	out := make([]contract.MetricDef, 0, len(metrics))
	for _, metric := range metrics {
		if metric == nil {
			out = append(out, contract.MetricDef{})
			continue
		}
		out = append(out, contract.MetricDef{Name: metric.GetName(), Type: metric.GetType()})
	}
	return out
}

func artifactsFromProto(artifacts []*protocol.ArtifactDef) []contract.ArtifactDef {
	out := make([]contract.ArtifactDef, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			out = append(out, contract.ArtifactDef{})
			continue
		}
		out = append(out, contract.ArtifactDef{Name: artifact.GetName(), ContentType: artifact.GetContentType()})
	}
	return out
}
