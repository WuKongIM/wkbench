// Package contract defines the stable unit API shared by the kernel and units.
package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Kind is a versioned unit identifier such as traffic.group_send/v1.
type Kind string

// PortType is a versioned capability identifier such as port.channel.group_set/v1.
type PortType string

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

// Labels are metric dimensions emitted by units.
type Labels map[string]string

// Definition describes a unit's stable contract.
type Definition struct {
	// Kind identifies the unit implementation and schema version.
	Kind string
	// Title is a short human-readable name.
	Title string
	// Description explains what the unit does.
	Description string
	// Inputs lists required data ports consumed by the unit.
	Inputs []PortDef
	// Outputs lists data ports produced by the unit.
	Outputs []PortDef
	// Metrics lists metric names emitted by the unit.
	Metrics []MetricDef
	// Artifacts lists artifact names written by the unit.
	Artifacts []ArtifactDef
}

// PortDef describes one named input or output port.
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

// MetricDef describes one metric emitted by a unit.
type MetricDef struct {
	// Name is the unit-local metric name.
	Name string
	// Type is the metric type, for example counter or histogram.
	Type string
}

// ArtifactDef describes one artifact emitted by a unit.
type ArtifactDef struct {
	// Name is the unit-local artifact name.
	Name string
	// ContentType is the MIME type written for this artifact when known.
	ContentType string
}

// ArtifactInfo describes an artifact produced through a RunEnv.
type ArtifactInfo struct {
	// Path is the artifact file path.
	Path string
	// ContentType is the declared MIME type when known.
	ContentType string
	// SizeBytes is the number of bytes written before Close.
	SizeBytes int64
}

// Unit is the standard interface implemented by every benchmark unit.
type Unit interface {
	// Definition returns the static unit contract.
	Definition() Definition
	// Validate checks local spec shape without network IO.
	Validate(context.Context, ValidateEnv) error
	// Plan computes deterministic execution work before runtime side effects.
	Plan(context.Context, PlanEnv) (Plan, error)
	// Run executes the unit and publishes outputs through RunEnv.
	Run(context.Context, RunEnv) error
}

// BackgroundUnit is an optional lifecycle for units that run while later graph nodes execute.
type BackgroundUnit interface {
	Unit
	// Start starts background work and returns when the unit is ready for downstream units.
	Start(context.Context, RunEnv) (BackgroundTask, error)
}

// BackgroundTask is the active background worker returned by a BackgroundUnit.
type BackgroundTask interface {
	// Done closes when the worker exits. Later kernel lifecycle code may use a received non-nil error as fatal.
	Done() <-chan error
	// Stop asks the worker to flush, publish final outputs, and exit.
	Stop(context.Context) error
}

// ValidateEnv is the environment available during unit validation.
type ValidateEnv interface {
	// UnitName returns the scenario-local unit name.
	UnitName() string
	// DecodeSpec decodes this unit's spec into out.
	DecodeSpec(out any) error
}

// PlanEnv is the environment available during planning.
type PlanEnv interface {
	ValidateEnv
	// RunID returns the scenario run identifier.
	RunID() string
	// RunDuration returns the configured measured run duration.
	RunDuration() time.Duration
	// WorkerCount returns the number of execution workers.
	WorkerCount() int
}

// RunEnv is the environment available during runtime execution.
type RunEnv interface {
	PlanEnv
	// Input returns one connected input port value by unit-local input name.
	Input(name string) (any, error)
	// SetOutput publishes a unit-local output port value.
	SetOutput(name string, value any) error
	// EmitCounter emits a counter delta for this unit.
	EmitCounter(name string, delta float64, labels Labels)
	// ObserveDuration records a duration metric sample for this unit.
	ObserveDuration(name string, value time.Duration, labels Labels)
	// NextID returns a deterministic per-unit identifier.
	NextID(prefix string) string
	// Payload returns deterministic payload bytes of size.
	Payload(size int) []byte
	// OpenArtifact opens a declared artifact for writing.
	OpenArtifact(name string) (io.WriteCloser, error)
}

// MetricSnapshotRecorder records aggregate metric snapshots when exact samples
// are unavailable, such as across the plugin process boundary.
type MetricSnapshotRecorder interface {
	RecordMetricSnapshot(MetricSnapshot)
}

// ReportableOutput allows output values to opt into JSON/Markdown reports.
type ReportableOutput interface {
	// ReportOutput returns a JSON-friendly, non-sensitive summary value.
	ReportOutput() any
}

// OutputWrapper is implemented by stored output decorators that should expose
// their raw value to downstream graph inputs.
type OutputWrapper interface {
	// OutputValue returns the unit-facing value represented by this output.
	OutputValue() any
}

// CloseableOutput is implemented by output values that own runtime resources.
type CloseableOutput interface {
	// Close releases runtime resources owned by the output value.
	Close() error
}

// Plan is a unit-owned deterministic execution plan.
type Plan struct {
	// UnitName is the scenario-local unit name.
	UnitName string `json:"unit_name,omitempty"`
	// Shards contains JSON-friendly shard descriptions.
	Shards []any `json:"shards,omitempty"`
}

// MetricSnapshot is an aggregate metric view recorded by TestRunEnv.
type MetricSnapshot struct {
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Labels Labels  `json:"labels,omitempty"`
	Count  int64   `json:"count"`
	Sum    float64 `json:"sum"`
	Min    float64 `json:"min,omitempty"`
	Max    float64 `json:"max,omitempty"`
}

// Rate represents a per-second operation rate.
type Rate struct {
	// PerSecond is the number of operations per second.
	PerSecond float64 `json:"per_second"`
}

// ParseRate parses strings such as "500/s" or "12.5/s".
func ParseRate(raw string) (Rate, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, "/sec")
	raw = strings.TrimSuffix(raw, "/s")
	if raw == "" {
		return Rate{}, fmt.Errorf("rate is empty")
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return Rate{}, fmt.Errorf("invalid rate %q: %w", raw, err)
	}
	return Rate{PerSecond: value}, nil
}

// UnmarshalJSON decodes either a JSON string like "500/s" or a number.
func (r *Rate) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		parsed, err := ParseRate(raw)
		if err != nil {
			return err
		}
		*r = parsed
		return nil
	}
	var number float64
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*r = Rate{PerSecond: number}
	return nil
}

// Duration wraps time.Duration with JSON/text decoding from Go duration strings.
type Duration struct {
	// Duration is the decoded time duration.
	time.Duration
}

// UnmarshalJSON decodes either a JSON duration string or a nanosecond number.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		parsed, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		d.Duration = parsed
		return nil
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return err
	}
	d.Duration = time.Duration(nanos)
	return nil
}

// Input returns a typed runtime input.
func Input[T any](env RunEnv, name string) (T, error) {
	var zero T
	value, err := env.Input(name)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return zero, fmt.Errorf("input %q has unexpected type %T and cannot encode as json: %w", name, value, marshalErr)
		}
		var decoded T
		if unmarshalErr := json.Unmarshal(payload, &decoded); unmarshalErr != nil {
			return zero, fmt.Errorf("input %q has unexpected type %T; decode as %T: %w", name, value, zero, unmarshalErr)
		}
		return decoded, nil
	}
	return typed, nil
}

// Output returns a typed output from an environment that exposes stored outputs.
func Output[T any](reader OutputReader, name string) (T, error) {
	var zero T
	value, ok := reader.Output(name)
	if !ok {
		return zero, fmt.Errorf("output %q not found", name)
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("output %q has unexpected type %T", name, value)
	}
	return typed, nil
}

// OutputReader is implemented by test and kernel run environments.
type OutputReader interface {
	// Output returns one stored output by unit-local output name.
	Output(name string) (any, bool)
}

// TestRunEnv is a small in-memory RunEnv useful for unit tests.
type TestRunEnv struct {
	runID        string
	unitName     string
	inputs       map[string]any
	spec         map[string]any
	outputs      map[string]any
	counters     map[string]float64
	durations    map[string][]time.Duration
	metrics      map[string]MetricSnapshot
	artifactDefs map[string]ArtifactDef
	artifacts    map[string]ArtifactInfo
	runDuration  time.Duration
	workerCount  int

	mu     sync.Mutex
	nextID int64
}

// NewTestRunEnv builds a RunEnv with fixed inputs and spec.
func NewTestRunEnv(runID, unitName string, inputs map[string]any, spec map[string]any) *TestRunEnv {
	return &TestRunEnv{
		runID:        runID,
		unitName:     unitName,
		inputs:       cloneMap(inputs),
		spec:         cloneMap(spec),
		outputs:      make(map[string]any),
		counters:     make(map[string]float64),
		durations:    make(map[string][]time.Duration),
		metrics:      make(map[string]MetricSnapshot),
		artifactDefs: make(map[string]ArtifactDef),
		artifacts:    make(map[string]ArtifactInfo),
		runDuration:  time.Second,
		workerCount:  1,
	}
}

// UnitName implements ValidateEnv.
func (e *TestRunEnv) UnitName() string { return e.unitName }

// RunID implements PlanEnv.
func (e *TestRunEnv) RunID() string { return e.runID }

// RunDuration implements PlanEnv.
func (e *TestRunEnv) RunDuration() time.Duration { return e.runDuration }

// SetRunDuration changes the test run duration.
func (e *TestRunEnv) SetRunDuration(d time.Duration) { e.runDuration = d }

// SetWorkerCount changes the test worker count. Non-positive values reset to the default.
func (e *TestRunEnv) SetWorkerCount(count int) {
	if count <= 0 {
		count = 1
	}
	e.workerCount = count
}

// WorkerCount implements PlanEnv.
func (e *TestRunEnv) WorkerCount() int {
	if e.workerCount <= 0 {
		return 1
	}
	return e.workerCount
}

// DecodeSpec implements ValidateEnv.
func (e *TestRunEnv) DecodeSpec(out any) error { return decodeMap(e.spec, out) }

// Input implements RunEnv.
func (e *TestRunEnv) Input(name string) (any, error) {
	value, ok := e.inputs[name]
	if !ok {
		return nil, fmt.Errorf("input %q not found", name)
	}
	return value, nil
}

// SetOutput implements RunEnv.
func (e *TestRunEnv) SetOutput(name string, value any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.outputs[name] = value
	return nil
}

// Output implements OutputReader.
func (e *TestRunEnv) Output(name string) (any, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	value, ok := e.outputs[name]
	return value, ok
}

// EmitCounter implements RunEnv.
func (e *TestRunEnv) EmitCounter(name string, delta float64, labels Labels) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counters[name] += delta
	key := metricSnapshotKey(name, labels)
	snapshot := e.metrics[key]
	if snapshot.Type == "" {
		snapshot.Name = name
		snapshot.Type = "counter"
		snapshot.Labels = cloneLabels(labels)
	}
	snapshot.Count++
	snapshot.Sum += delta
	e.metrics[key] = snapshot
}

// CounterValue returns the current test counter value.
func (e *TestRunEnv) CounterValue(name string) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counters[name]
}

// ObserveDuration implements RunEnv.
func (e *TestRunEnv) ObserveDuration(name string, value time.Duration, labels Labels) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.durations[name] = append(e.durations[name], value)
	seconds := value.Seconds()
	key := metricSnapshotKey(name, labels)
	snapshot := e.metrics[key]
	if snapshot.Type == "" {
		snapshot.Name = name
		snapshot.Type = "duration"
		snapshot.Labels = cloneLabels(labels)
		snapshot.Min = seconds
		snapshot.Max = seconds
	} else {
		if seconds < snapshot.Min {
			snapshot.Min = seconds
		}
		if seconds > snapshot.Max {
			snapshot.Max = seconds
		}
	}
	snapshot.Count++
	snapshot.Sum += seconds
	e.metrics[key] = snapshot
}

// DurationValues returns recorded duration samples for name.
func (e *TestRunEnv) DurationValues(name string) []time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	values := e.durations[name]
	return append([]time.Duration(nil), values...)
}

// MetricSnapshots returns aggregate metrics emitted by this environment.
func (e *TestRunEnv) MetricSnapshots() []MetricSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.metrics) == 0 {
		return nil
	}
	keys := make([]string, 0, len(e.metrics))
	for key := range e.metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]MetricSnapshot, 0, len(keys))
	for _, key := range keys {
		snapshot := e.metrics[key]
		snapshot.Labels = cloneLabels(snapshot.Labels)
		out = append(out, snapshot)
	}
	return out
}

// RecordMetricSnapshot records an aggregate metric snapshot.
func (e *TestRunEnv) RecordMetricSnapshot(snapshot MetricSnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if snapshot.Count <= 0 {
		return
	}
	snapshot.Labels = cloneLabels(snapshot.Labels)
	e.metrics[metricSnapshotKey(snapshot.Name, snapshot.Labels)] = snapshot
}

func metricSnapshotKey(name string, labels Labels) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(labels[key]))
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

func cloneLabels(labels Labels) Labels {
	if len(labels) == 0 {
		return nil
	}
	clone := make(Labels, len(labels))
	for key, value := range labels {
		clone[key] = value
	}
	return clone
}

// DeclareArtifacts sets the artifact declarations accepted by OpenArtifact.
func (e *TestRunEnv) DeclareArtifacts(defs []ArtifactDef) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artifactDefs = make(map[string]ArtifactDef, len(defs))
	for _, def := range defs {
		e.artifactDefs[def.Name] = def
	}
	if e.artifacts == nil {
		e.artifacts = make(map[string]ArtifactInfo)
	}
}

// Artifacts returns a copy of artifact information recorded on Close.
func (e *TestRunEnv) Artifacts() map[string]ArtifactInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.artifacts) == 0 {
		return nil
	}
	out := make(map[string]ArtifactInfo, len(e.artifacts))
	for name, info := range e.artifacts {
		out[name] = info
	}
	return out
}

// OpenArtifact implements RunEnv.
func (e *TestRunEnv) OpenArtifact(name string) (io.WriteCloser, error) {
	e.mu.Lock()
	def, ok := e.artifactDefs[name]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("artifact %q not declared", name)
	}
	if err := validateArtifactName(name); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "wkbench-artifact-*")
	if err != nil {
		return nil, err
	}
	return &testArtifactWriter{env: e, name: name, file: file, contentType: def.ContentType}, nil
}

func validateArtifactName(name string) error {
	if name == "" {
		return fmt.Errorf("artifact name is required")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || containsWhitespace(name) || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("artifact %q must be a simple relative file name", name)
	}
	return nil
}

func containsWhitespace(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// NextID implements RunEnv.
func (e *TestRunEnv) NextID(prefix string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	if prefix == "" {
		prefix = "id"
	}
	return fmt.Sprintf("%s-%d", prefix, e.nextID)
}

// Payload implements RunEnv.
func (e *TestRunEnv) Payload(size int) []byte {
	if size <= 0 {
		return nil
	}
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	return payload
}

type testArtifactWriter struct {
	env         *TestRunEnv
	name        string
	file        *os.File
	contentType string
	size        int64
	closed      bool
	closeErr    error
}

func (w *testArtifactWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *testArtifactWriter) Close() error {
	if w.closed {
		return w.closeErr
	}
	closeErr := w.file.Close()
	w.recordArtifact()
	w.closed = true
	w.closeErr = closeErr
	return closeErr
}

func (w *testArtifactWriter) recordArtifact() {
	w.env.mu.Lock()
	defer w.env.mu.Unlock()
	if w.env.artifacts == nil {
		w.env.artifacts = make(map[string]ArtifactInfo)
	}
	w.env.artifacts[w.name] = ArtifactInfo{
		Path:        w.file.Name(),
		ContentType: w.contentType,
		SizeBytes:   w.size,
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func decodeMap(in map[string]any, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}
