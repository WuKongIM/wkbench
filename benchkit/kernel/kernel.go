// Package kernel validates and executes wkbench unit graphs.
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
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
}

// OutputResult summarizes one produced output port.
type OutputResult struct {
	// Type is the versioned public port type.
	Type contract.PortType `json:"type"`
	// Value is present only when the output opted into reports.
	Value any `json:"value,omitempty"`
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

// Run validates, plans, and executes a scenario graph.
func (e *Engine) Run(ctx context.Context, scenario dsl.Scenario) (Result, error) {
	result := Result{RunID: scenario.Run.ID, Status: StatusCompleted, Units: make(map[string]UnitResult, len(scenario.Units))}
	graph, err := e.buildGraph(scenario)
	if err != nil {
		result.Status = StatusConfigFailed
		return result, err
	}
	outputs := newOutputStore()
	for _, name := range graph.order {
		node := graph.nodes[name]
		base := newBaseEnv(scenario, name, node.dsl.Spec)
		if err := node.unit.Validate(ctx, base); err != nil {
			result.Status = StatusConfigFailed
			result.Units[name] = UnitResult{Kind: node.def.Kind, Status: StatusConfigFailed, Error: err.Error()}
			return result, fmt.Errorf("unit %q validate: %w", name, err)
		}
		if _, err := node.unit.Plan(ctx, base); err != nil {
			result.Status = StatusPlanFailed
			result.Units[name] = UnitResult{Kind: node.def.Kind, Status: StatusPlanFailed, Error: err.Error()}
			return result, fmt.Errorf("unit %q plan: %w", name, err)
		}
		env := &runEnv{
			baseEnv:  base,
			graph:    graph,
			unitName: name,
			outputs:  outputs,
			counters: make(map[string]float64),
		}
		if err := node.unit.Run(ctx, env); err != nil {
			result.Status = StatusWorkerFailed
			result.Units[name] = UnitResult{Kind: node.def.Kind, Status: StatusWorkerFailed, Error: err.Error()}
			return result, fmt.Errorf("unit %q run: %w", name, err)
		}
		result.Units[name] = UnitResult{Kind: node.def.Kind, Status: StatusCompleted, Outputs: outputs.resultsForUnit(name, node.def.Outputs)}
	}
	return result, nil
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
	counters map[string]float64
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
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counters[name] += delta
}

func (e *runEnv) ObserveDuration(name string, value time.Duration, labels contract.Labels) {}

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
