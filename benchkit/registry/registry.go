// Package registry stores unit implementations available to a wkbench build.
package registry

import (
	"fmt"
	"sort"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

// Registry resolves unit kinds to implementations.
type Registry struct {
	units map[string]contract.Unit
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{units: make(map[string]contract.Unit)}
}

// Register adds a unit implementation.
func (r *Registry) Register(unit contract.Unit) error {
	if unit == nil {
		return fmt.Errorf("unit is nil")
	}
	def := unit.Definition()
	kind := strings.TrimSpace(def.Kind)
	if kind == "" {
		return fmt.Errorf("unit kind is required")
	}
	if _, ok := r.units[kind]; ok {
		return fmt.Errorf("unit kind %q is already registered", kind)
	}
	r.units[kind] = unit
	return nil
}

// MustRegister adds a unit implementation or panics.
func (r *Registry) MustRegister(unit contract.Unit) {
	if err := r.Register(unit); err != nil {
		panic(err)
	}
}

// Resolve finds a unit by exact kind or by unique unversioned kind prefix.
func (r *Registry) Resolve(kind string) (contract.Unit, contract.Definition, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return nil, contract.Definition{}, fmt.Errorf("unit kind is required")
	}
	if unit, ok := r.units[kind]; ok {
		return unit, unit.Definition(), nil
	}
	prefix := kind + "/"
	matches := make([]string, 0, 1)
	for registered := range r.units {
		if strings.HasPrefix(registered, prefix) {
			matches = append(matches, registered)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return nil, contract.Definition{}, fmt.Errorf("unit kind %q is not registered", kind)
	case 1:
		unit := r.units[matches[0]]
		return unit, unit.Definition(), nil
	default:
		return nil, contract.Definition{}, fmt.Errorf("unit kind %q is ambiguous: %s", kind, strings.Join(matches, ", "))
	}
}

// Definitions returns registered unit definitions sorted by kind.
func (r *Registry) Definitions() []contract.Definition {
	kinds := make([]string, 0, len(r.units))
	for kind := range r.units {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	defs := make([]contract.Definition, 0, len(kinds))
	for _, kind := range kinds {
		defs = append(defs, r.units[kind].Definition())
	}
	return defs
}
