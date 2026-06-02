// Package kernel validates and executes wkbench unit graphs.
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/dsl"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

// Status is the terminal scenario result status.
type Status string

const (
	// StatusCompleted means every unit completed.
	StatusCompleted Status = "completed"
	// StatusConfigFailed means graph or spec validation failed.
	StatusConfigFailed Status = "config_failed"
	// StatusPlanFailed means deterministic planning failed.
	StatusPlanFailed Status = "plan_failed"
	// StatusWorkerFailed means a unit run failed.
	StatusWorkerFailed Status = "worker_failed"
)

// Engine validates, plans, and executes scenario graphs.
type Engine struct {
	reg *registry.Registry
}

// New creates a graph engine backed by reg.
func New(reg *registry.Registry) *Engine {
	return &Engine{reg: reg}
}

// Result summarizes one scenario execution.
type Result struct {
	// RunID is copied from the scenario.
	RunID string `json:"run_id"`
	// Status is the terminal scenario status.
	Status Status `json:"status"`
	// Units contains per-unit execution results.
	Units map[string]UnitResult `json:"units"`
}

// UnitResult summarizes one unit execution.
type UnitResult struct {
	// Kind is the resolved unit kind.
	Kind string `json:"kind"`
	// Status is the unit status.
	Status Status `json:"status"`
	// Error records the terminal error when present.
	Error string `json:"error,omitempty"`
	// Outputs lists output ports produced by the unit.
	Outputs map[string]OutputResult `json:"outputs,omitempty"`
	// Metrics lists aggregated metrics emitted by the unit.
	Metrics map[string]MetricResult `json:"metrics,omitempty"`
	// StartedAt is the RFC3339Nano timestamp when runtime work started.
	StartedAt string `json:"started_at,omitempty"`
	// EndedAt is the RFC3339Nano timestamp when runtime work ended.
	EndedAt string `json:"ended_at,omitempty"`
	// ElapsedMS is the runtime elapsed wall time in milliseconds.
	ElapsedMS int64 `json:"elapsed_ms,omitempty"`
	// Cleanup lists non-fatal cleanup results for closeable outputs.
	Cleanup []CleanupResult `json:"cleanup,omitempty"`
}

// PlanResult summarizes one non-executing scenario planning pass.
type PlanResult struct {
	// RunID is copied from the scenario.
	RunID string `json:"run_id"`
	// Status is the terminal planning status.
	Status Status `json:"status"`
	// Order is the deterministic unit execution order.
	Order []string `json:"order"`
	// Units contains per-unit planning results.
	Units map[string]UnitPlanResult `json:"units"`
	// Wiring lists resolved input bindings in execution order.
	Wiring []ExplainBinding `json:"wiring,omitempty"`
}

// UnitPlanResult summarizes one unit planning result.
type UnitPlanResult struct {
	// Kind is the resolved unit kind.
	Kind string `json:"kind"`
	// Status is the unit planning status.
	Status Status `json:"status"`
	// Error records the terminal validation or planning error when present.
	Error string `json:"error,omitempty"`
	// Plan is the unit-owned deterministic execution plan.
	Plan contract.Plan `json:"plan,omitempty"`
}

// OutputResult summarizes one produced output port.
type OutputResult struct {
	// Type is the versioned public port type.
	Type contract.PortType `json:"type"`
	// Value is present only when the output opted into reports.
	Value any `json:"value,omitempty"`
}

// MetricResult summarizes one aggregated unit metric.
type MetricResult struct {
	// Type is the metric kind, for example counter or duration.
	Type string `json:"type"`
	// Labels are the metric dimensions for labelled emissions.
	Labels contract.Labels `json:"labels,omitempty"`
	// Count is the number of emitted samples.
	Count int64 `json:"count"`
	// Sum is the accumulated value. Durations are recorded in seconds.
	Sum float64 `json:"sum"`
	// Min is the minimum observed value for duration metrics.
	Min float64 `json:"min,omitempty"`
	// Max is the maximum observed value for duration metrics.
	Max float64 `json:"max,omitempty"`
	// P95 is the nearest-rank 95th percentile for duration metrics.
	P95 float64 `json:"p95,omitempty"`
	// P99 is the nearest-rank 99th percentile for duration metrics.
	P99 float64 `json:"p99,omitempty"`
}

// CleanupResult summarizes cleanup for one produced output.
type CleanupResult struct {
	// Output is the unit-local output port name.
	Output string `json:"output"`
	// Error records a non-fatal cleanup error when present.
	Error string `json:"error,omitempty"`
}

// Explanation describes a scenario graph without executing it.
type Explanation struct {
	// RunID is copied from the scenario.
	RunID string `json:"run_id"`
	// Order is the deterministic unit execution order.
	Order []string `json:"order"`
	// Units contains resolved unit contracts keyed by scenario-local name.
	Units map[string]ExplainUnit `json:"units"`
	// Wiring lists resolved input bindings in execution order.
	Wiring []ExplainBinding `json:"wiring,omitempty"`
}

// ExplainUnit describes one resolved unit in an explanation.
type ExplainUnit struct {
	// Kind is the resolved versioned unit kind.
	Kind string `json:"kind"`
	// Inputs lists declared input ports.
	Inputs []ExplainPort `json:"inputs,omitempty"`
	// Outputs lists declared output ports.
	Outputs []ExplainPort `json:"outputs,omitempty"`
	// After lists explicit scenario ordering dependencies.
	After []string `json:"after,omitempty"`
}

// ExplainPort describes one input or output port in an explanation.
type ExplainPort struct {
	// Name is the unit-local port name.
	Name string `json:"name"`
	// Type is the versioned public port type.
	Type contract.PortType `json:"type"`
	// Optional means the input may be omitted.
	Optional bool `json:"optional,omitempty"`
}

// ExplainBinding describes one resolved input binding.
type ExplainBinding struct {
	// Unit is the scenario-local consuming unit name.
	Unit string `json:"unit"`
	// Input is the consuming unit-local input port name.
	Input string `json:"input"`
	// SourceUnit is the scenario-local producing unit name.
	SourceUnit string `json:"source_unit"`
	// SourceOutput is the producing unit-local output port name.
	SourceOutput string `json:"source_output"`
	// Type is the versioned public port type shared by both ports.
	Type contract.PortType `json:"type"`
}

// Validate checks graph wiring and unit specs without executing units.
func (e *Engine) Validate(ctx context.Context, scenario dsl.Scenario) error {
	graph, err := e.buildGraph(scenario)
	if err != nil {
		return err
	}
	for _, name := range graph.order {
		node := graph.nodes[name]
		if err := node.unit.Validate(ctx, newBaseEnv(scenario, name, node.dsl.Spec)); err != nil {
			return fmt.Errorf("unit %q validate: %w", name, err)
		}
	}
	return nil
}

// Explain checks graph wiring and unit specs, then returns a non-executing graph summary.
func (e *Engine) Explain(ctx context.Context, scenario dsl.Scenario) (Explanation, error) {
	graph, err := e.buildGraph(scenario)
	if err != nil {
		return Explanation{}, err
	}
	explanation := Explanation{
		RunID: scenario.Run.ID,
		Order: append([]string(nil), graph.order...),
		Units: make(map[string]ExplainUnit, len(graph.nodes)),
	}
	for _, name := range graph.order {
		node := graph.nodes[name]
		if err := node.unit.Validate(ctx, newBaseEnv(scenario, name, node.dsl.Spec)); err != nil {
			return Explanation{}, fmt.Errorf("unit %q validate: %w", name, err)
		}
		explanation.Units[name] = ExplainUnit{
			Kind:    node.def.Kind,
			Inputs:  explainPorts(node.def.Inputs),
			Outputs: explainPorts(node.def.Outputs),
			After:   append([]string(nil), node.after...),
		}
	}
	explanation.Wiring = graphWiring(graph)
	return explanation, nil
}

// Plan validates and materializes deterministic unit plans without executing units.
func (e *Engine) Plan(ctx context.Context, scenario dsl.Scenario) (PlanResult, error) {
	result := PlanResult{RunID: scenario.Run.ID, Status: StatusCompleted, Units: make(map[string]UnitPlanResult, len(scenario.Units))}
	graph, err := e.buildGraph(scenario)
	if err != nil {
		result.Status = StatusConfigFailed
		return result, err
	}
	result.Order = append([]string(nil), graph.order...)
	result.Wiring = graphWiring(graph)
	for _, name := range graph.order {
		node := graph.nodes[name]
		base := newBaseEnv(scenario, name, node.dsl.Spec)
		if err := node.unit.Validate(ctx, base); err != nil {
			result.Status = StatusConfigFailed
			result.Units[name] = UnitPlanResult{Kind: node.def.Kind, Status: StatusConfigFailed, Error: err.Error()}
			return result, fmt.Errorf("unit %q validate: %w", name, err)
		}
		plan, err := node.unit.Plan(ctx, base)
		if err != nil {
			result.Status = StatusPlanFailed
			result.Units[name] = UnitPlanResult{Kind: node.def.Kind, Status: StatusPlanFailed, Error: err.Error()}
			return result, fmt.Errorf("unit %q plan: %w", name, err)
		}
		result.Units[name] = UnitPlanResult{Kind: node.def.Kind, Status: StatusCompleted, Plan: plan}
	}
	return result, nil
}

// Run validates, plans, and executes a scenario graph.
func (e *Engine) Run(ctx context.Context, scenario dsl.Scenario) (Result, error) {
	result := Result{RunID: scenario.Run.ID, Status: StatusCompleted, Units: make(map[string]UnitResult, len(scenario.Units))}
	graph, err := e.buildGraph(scenario)
	if err != nil {
		result.Status = StatusConfigFailed
		return result, err
	}
	outputs := newOutputStore()
	cleanup := func() {
		outputs.cleanup(graph.order, result.Units)
	}
	var active []activeBackground
	for _, name := range graph.order {
		node := graph.nodes[name]
		base := newBaseEnv(scenario, name, node.dsl.Spec)
		if err := node.unit.Validate(ctx, base); err != nil {
			result.Status = StatusConfigFailed
			result.Units[name] = UnitResult{Kind: node.def.Kind, Status: StatusConfigFailed, Error: err.Error()}
			stopBackgrounds(ctx, active, outputs, result.Units)
			cleanup()
			return result, fmt.Errorf("unit %q validate: %w", name, err)
		}
		if _, err := node.unit.Plan(ctx, base); err != nil {
			result.Status = StatusPlanFailed
			result.Units[name] = UnitResult{Kind: node.def.Kind, Status: StatusPlanFailed, Error: err.Error()}
			stopBackgrounds(ctx, active, outputs, result.Units)
			cleanup()
			return result, fmt.Errorf("unit %q plan: %w", name, err)
		}
		env := &runEnv{
			baseEnv:  base,
			graph:    graph,
			unitName: name,
			outputs:  outputs,
			metrics:  newMetricStore(node.def.Metrics),
		}
		if background, ok := node.unit.(contract.BackgroundUnit); ok {
			start := time.Now()
			task, err := background.Start(ctx, env)
			end := time.Now()
			if err != nil {
				startedAt, endedAt, elapsedMS := timelineFields(start, end)
				result.Status = StatusWorkerFailed
				result.Units[name] = UnitResult{
					Kind:      node.def.Kind,
					Status:    StatusWorkerFailed,
					Error:     err.Error(),
					Metrics:   env.metrics.results(),
					StartedAt: startedAt,
					EndedAt:   endedAt,
					ElapsedMS: elapsedMS,
				}
				stopBackgrounds(ctx, active, outputs, result.Units)
				cleanup()
				return result, fmt.Errorf("unit %q start: %w", name, err)
			}
			active = append(active, activeBackground{name: name, node: node, env: env, task: task, startedAt: start})
			continue
		}
		start := time.Now()
		err := node.unit.Run(ctx, env)
		end := time.Now()
		startedAt, endedAt, elapsedMS := timelineFields(start, end)
		if err != nil {
			result.Status = StatusWorkerFailed
			result.Units[name] = UnitResult{
				Kind:      node.def.Kind,
				Status:    StatusWorkerFailed,
				Error:     err.Error(),
				Metrics:   env.metrics.results(),
				StartedAt: startedAt,
				EndedAt:   endedAt,
				ElapsedMS: elapsedMS,
			}
			stopBackgrounds(ctx, active, outputs, result.Units)
			cleanup()
			return result, fmt.Errorf("unit %q run: %w", name, err)
		}
		result.Units[name] = UnitResult{
			Kind:      node.def.Kind,
			Status:    StatusCompleted,
			Outputs:   outputs.resultsForUnit(name, node.def.Outputs),
			Metrics:   env.metrics.results(),
			StartedAt: startedAt,
			EndedAt:   endedAt,
			ElapsedMS: elapsedMS,
		}
	}
	if err := stopBackgrounds(ctx, active, outputs, result.Units); err != nil {
		result.Status = StatusWorkerFailed
		cleanup()
		return result, err
	}
	cleanup()
	return result, nil
}

func stopBackgrounds(ctx context.Context, active []activeBackground, outputs *outputStore, results map[string]UnitResult) error {
	var firstErr error
	for i := len(active) - 1; i >= 0; i-- {
		bg := active[i]
		err := bg.task.Stop(ctx)
		end := time.Now()
		startedAt, endedAt, elapsedMS := timelineFields(bg.startedAt, end)
		status := StatusCompleted
		errorText := ""
		if err != nil {
			status = StatusWorkerFailed
			errorText = err.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("unit %q stop: %w", bg.name, err)
			}
		}
		results[bg.name] = UnitResult{
			Kind:      bg.node.def.Kind,
			Status:    status,
			Error:     errorText,
			Outputs:   outputs.resultsForUnit(bg.name, bg.node.def.Outputs),
			Metrics:   bg.env.metrics.results(),
			StartedAt: startedAt,
			EndedAt:   endedAt,
			ElapsedMS: elapsedMS,
		}
	}
	return firstErr
}

func graphWiring(graph *graph) []ExplainBinding {
	var wiring []ExplainBinding
	for _, name := range graph.order {
		node := graph.nodes[name]
		for _, input := range node.def.Inputs {
			ref, ok := node.bindings[input.Name]
			if !ok {
				continue
			}
			wiring = append(wiring, ExplainBinding{
				Unit:         name,
				Input:        input.Name,
				SourceUnit:   ref.unit,
				SourceOutput: ref.port,
				Type:         input.Type,
			})
		}
	}
	return wiring
}

func explainPorts(defs []contract.PortDef) []ExplainPort {
	if len(defs) == 0 {
		return nil
	}
	ports := make([]ExplainPort, 0, len(defs))
	for _, def := range defs {
		ports = append(ports, ExplainPort{Name: def.Name, Type: def.Type, Optional: def.Optional})
	}
	return ports
}

func timelineFields(start, end time.Time) (string, string, int64) {
	elapsed := end.Sub(start).Milliseconds()
	if elapsed < 1 && end.After(start) {
		elapsed = 1
	}
	return start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), elapsed
}

type graph struct {
	nodes map[string]*graphNode
	order []string
}

type graphNode struct {
	name     string
	dsl      dsl.UnitNode
	unit     contract.Unit
	def      contract.Definition
	bindings map[string]resourceRef
	after    []string
}

type activeBackground struct {
	name      string
	node      *graphNode
	env       *runEnv
	task      contract.BackgroundTask
	startedAt time.Time
}

type resourceRef struct {
	unit string
	port string
}

type provider struct {
	ref  resourceRef
	port contract.PortDef
}

func (e *Engine) buildGraph(scenario dsl.Scenario) (*graph, error) {
	if strings.TrimSpace(scenario.Version) != "wkbench/v2" {
		return nil, fmt.Errorf("scenario.version must be wkbench/v2")
	}
	if len(scenario.Units) == 0 {
		return nil, fmt.Errorf("scenario.units is required")
	}
	g := &graph{nodes: make(map[string]*graphNode, len(scenario.Units))}
	names := sortedUnitNames(scenario.Units)
	for _, name := range names {
		nodeSpec := scenario.Units[name]
		unit, def, err := e.reg.Resolve(nodeSpec.Use)
		if err != nil {
			return nil, fmt.Errorf("unit %q: %w", name, err)
		}
		g.nodes[name] = &graphNode{name: name, dsl: nodeSpec, unit: unit, def: def, bindings: make(map[string]resourceRef)}
	}
	providers := make([]provider, 0)
	for _, name := range names {
		node := g.nodes[name]
		for _, out := range node.def.Outputs {
			providers = append(providers, provider{ref: resourceRef{unit: name, port: out.Name}, port: out})
		}
	}
	for _, name := range names {
		node := g.nodes[name]
		for _, input := range node.def.Inputs {
			ref, err := resolveInput(name, input, node.dsl.Inputs, providers)
			if err != nil {
				return nil, err
			}
			if ref.unit != "" {
				node.bindings[input.Name] = ref
			}
		}
		node.after = append(node.after, node.dsl.After...)
	}
	order, err := topoSort(g.nodes)
	if err != nil {
		return nil, err
	}
	g.order = order
	return g, nil
}

func resolveInput(unitName string, input contract.PortDef, explicit map[string]string, providers []provider) (resourceRef, error) {
	if explicit != nil {
		if raw, ok := explicit[input.Name]; ok {
			ref, err := parseRef(raw)
			if err != nil {
				return resourceRef{}, fmt.Errorf("unit %q input %q: %w", unitName, input.Name, err)
			}
			for _, p := range providers {
				if p.ref == ref {
					if p.port.Type != input.Type {
						return resourceRef{}, fmt.Errorf("unit %q input %q expects %s but %s.%s provides %s", unitName, input.Name, input.Type, ref.unit, ref.port, p.port.Type)
					}
					return ref, nil
				}
			}
			return resourceRef{}, fmt.Errorf("unit %q input %q references unknown output %q", unitName, input.Name, raw)
		}
	}
	candidates := make([]provider, 0, 1)
	for _, p := range providers {
		if p.ref.unit == unitName {
			continue
		}
		if p.port.Type == input.Type {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		if input.Optional {
			return resourceRef{}, nil
		}
		return resourceRef{}, fmt.Errorf("unit %q input %q expects %s but no provider exists", unitName, input.Name, input.Type)
	}
	if len(candidates) > 1 {
		refs := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			refs = append(refs, candidate.ref.String())
		}
		sort.Strings(refs)
		return resourceRef{}, fmt.Errorf("unit %q input %q expects %s but auto-wire is ambiguous: %s", unitName, input.Name, input.Type, strings.Join(refs, ", "))
	}
	return candidates[0].ref, nil
}

func parseRef(raw string) (resourceRef, error) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return resourceRef{}, fmt.Errorf("resource reference must be <unit>.<output>")
	}
	return resourceRef{unit: parts[0], port: parts[1]}, nil
}

func (r resourceRef) String() string {
	return r.unit + "." + r.port
}

func topoSort(nodes map[string]*graphNode) ([]string, error) {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	permanent := make(map[string]bool, len(nodes))
	temporary := make(map[string]bool, len(nodes))
	order := make([]string, 0, len(nodes))
	var visit func(string) error
	visit = func(name string) error {
		node, ok := nodes[name]
		if !ok {
			return fmt.Errorf("unit %q does not exist", name)
		}
		if permanent[name] {
			return nil
		}
		if temporary[name] {
			return fmt.Errorf("unit graph has a cycle at %q", name)
		}
		temporary[name] = true
		deps := make([]string, 0, len(node.bindings)+len(node.after))
		for _, ref := range node.bindings {
			deps = append(deps, ref.unit)
		}
		deps = append(deps, node.after...)
		sort.Strings(deps)
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		temporary[name] = false
		permanent[name] = true
		order = append(order, name)
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func sortedUnitNames(units map[string]dsl.UnitNode) []string {
	names := make([]string, 0, len(units))
	for name := range units {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type baseEnv struct {
	scenario dsl.Scenario
	unitName string
	spec     map[string]any
}

func newBaseEnv(scenario dsl.Scenario, unitName string, spec map[string]any) *baseEnv {
	if spec == nil {
		spec = make(map[string]any)
	}
	return &baseEnv{scenario: scenario, unitName: unitName, spec: spec}
}

func (e *baseEnv) UnitName() string { return e.unitName }

func (e *baseEnv) RunID() string { return e.scenario.Run.ID }

func (e *baseEnv) RunDuration() time.Duration { return e.scenario.Run.Duration }

func (e *baseEnv) WorkerCount() int { return 1 }

func (e *baseEnv) DecodeSpec(out any) error {
	data, err := json.Marshal(e.spec)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

type runEnv struct {
	*baseEnv
	graph    *graph
	unitName string
	outputs  *outputStore
	metrics  *metricStore
	nextID   int64
	mu       sync.Mutex
}

func (e *runEnv) Input(name string) (any, error) {
	node := e.graph.nodes[e.unitName]
	ref, ok := node.bindings[name]
	if !ok {
		return nil, fmt.Errorf("input %q is not wired", name)
	}
	return e.outputs.get(ref.unit, ref.port)
}

func (e *runEnv) SetOutput(name string, value any) error {
	e.outputs.set(e.unitName, name, value)
	return nil
}

func (e *runEnv) EmitCounter(name string, delta float64, labels contract.Labels) {
	e.metrics.addCounter(name, delta, labels)
}

func (e *runEnv) ObserveDuration(name string, value time.Duration, labels contract.Labels) {
	e.metrics.observeDuration(name, value, labels)
}

type metricStore struct {
	mu              sync.Mutex
	types           map[string]string
	items           map[string]MetricResult
	durationSamples map[string][]float64
}

func newMetricStore(defs []contract.MetricDef) *metricStore {
	types := make(map[string]string, len(defs))
	for _, def := range defs {
		if strings.TrimSpace(def.Name) == "" {
			continue
		}
		types[def.Name] = strings.TrimSpace(def.Type)
	}
	return &metricStore{
		types:           types,
		items:           make(map[string]MetricResult),
		durationSamples: make(map[string][]float64),
	}
}

func (s *metricStore) addCounter(name string, delta float64, labels contract.Labels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := metricKey(name, labels)
	item := s.items[key]
	if item.Type == "" {
		item.Type = metricType(s.types[name], "counter")
		item.Labels = cloneLabels(labels)
	}
	item.Count++
	item.Sum += delta
	s.items[key] = item
}

func (s *metricStore) observeDuration(name string, value time.Duration, labels contract.Labels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seconds := value.Seconds()
	key := metricKey(name, labels)
	item := s.items[key]
	if item.Type == "" {
		item.Type = metricType(s.types[name], "duration")
		item.Labels = cloneLabels(labels)
		item.Min = seconds
		item.Max = seconds
	} else {
		if seconds < item.Min {
			item.Min = seconds
		}
		if seconds > item.Max {
			item.Max = seconds
		}
	}
	item.Count++
	item.Sum += seconds
	s.items[key] = item
	s.durationSamples[key] = append(s.durationSamples[key], seconds)
}

func (s *metricStore) results() map[string]MetricResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 && len(s.types) == 0 {
		return nil
	}
	results := make(map[string]MetricResult, len(s.items)+len(s.types))
	emittedNames := make(map[string]bool, len(s.items))
	for key, item := range s.items {
		if item.Type == "duration" {
			item.P95 = percentileNearestRank(s.durationSamples[key], 95)
			item.P99 = percentileNearestRank(s.durationSamples[key], 99)
		}
		results[key] = item
		emittedNames[metricNameFromKey(key)] = true
	}
	for name, typ := range s.types {
		if emittedNames[name] {
			continue
		}
		results[name] = MetricResult{Type: metricType(typ, "counter")}
	}
	return results
}

func percentileNearestRank(values []float64, percent int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := (percent*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func metricType(declared string, fallback string) string {
	if strings.TrimSpace(declared) == "" {
		return fallback
	}
	return declared
}

func metricKey(name string, labels contract.Labels) string {
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

func metricNameFromKey(key string) string {
	if index := strings.IndexByte(key, '{'); index >= 0 {
		return key[:index]
	}
	return key
}

func cloneLabels(labels contract.Labels) contract.Labels {
	if len(labels) == 0 {
		return nil
	}
	clone := make(contract.Labels, len(labels))
	for key, value := range labels {
		clone[key] = value
	}
	return clone
}

func (e *runEnv) NextID(prefix string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	if prefix == "" {
		prefix = "id"
	}
	return fmt.Sprintf("%s-%d", prefix, e.nextID)
}

func (e *runEnv) Payload(size int) []byte {
	if size <= 0 {
		return nil
	}
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	return payload
}

type outputStore struct {
	mu     sync.Mutex
	values map[string]map[string]any
}

func newOutputStore() *outputStore {
	return &outputStore{values: make(map[string]map[string]any)}
}

func (s *outputStore) set(unit, port string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values[unit] == nil {
		s.values[unit] = make(map[string]any)
	}
	s.values[unit][port] = value
}

func (s *outputStore) get(unit, port string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ports := s.values[unit]
	if ports == nil {
		return nil, fmt.Errorf("unit %q has not produced outputs", unit)
	}
	value, ok := ports[port]
	if !ok {
		return nil, fmt.Errorf("unit %q output %q not found", unit, port)
	}
	return value, nil
}

func (s *outputStore) resultsForUnit(unit string, defs []contract.PortDef) map[string]OutputResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	ports := s.values[unit]
	if len(ports) == 0 {
		return nil
	}
	byName := make(map[string]contract.PortDef, len(defs))
	for _, def := range defs {
		byName[def.Name] = def
	}
	results := make(map[string]OutputResult, len(ports))
	for name, value := range ports {
		def, ok := byName[name]
		if !ok {
			continue
		}
		output := OutputResult{Type: def.Type}
		if reportable, ok := value.(contract.ReportableOutput); ok {
			output.Value = reportable.ReportOutput()
		}
		results[name] = output
	}
	if len(results) == 0 {
		return nil
	}
	return results
}

func (s *outputStore) cleanup(order []string, results map[string]UnitResult) {
	targets := s.cleanupTargets(order)
	for _, target := range targets {
		if err := target.closeable.Close(); err != nil {
			unitResult := results[target.unit]
			unitResult.Cleanup = append(unitResult.Cleanup, CleanupResult{Output: target.port, Error: err.Error()})
			results[target.unit] = unitResult
		}
	}
}

type cleanupTarget struct {
	unit      string
	port      string
	closeable contract.CloseableOutput
}

func (s *outputStore) cleanupTargets(order []string) []cleanupTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	var targets []cleanupTarget
	for i := len(order) - 1; i >= 0; i-- {
		unit := order[i]
		ports := s.values[unit]
		if len(ports) == 0 {
			continue
		}
		portNames := make([]string, 0, len(ports))
		for port := range ports {
			portNames = append(portNames, port)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(portNames)))
		for _, port := range portNames {
			closeable, ok := ports[port].(contract.CloseableOutput)
			if !ok {
				continue
			}
			targets = append(targets, cleanupTarget{unit: unit, port: port, closeable: closeable})
		}
	}
	return targets
}
