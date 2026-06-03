package pluginhost

import (
	"fmt"
	"sort"
	"strings"
)

type Catalog struct {
	unitsByKind map[string][]Unit
}

func NewCatalog(plugins []Plugin) *Catalog {
	unitsByKind := make(map[string][]Unit)
	for _, plugin := range plugins {
		for _, unit := range plugin.Units {
			unit.PluginName = plugin.Name
			unitsByKind[unit.Kind] = append(unitsByKind[unit.Kind], unit)
		}
	}
	for kind := range unitsByKind {
		sort.Slice(unitsByKind[kind], func(i, j int) bool {
			return unitsByKind[kind][i].PluginName < unitsByKind[kind][j].PluginName
		})
	}
	return &Catalog{unitsByKind: unitsByKind}
}

func (c *Catalog) Resolve(use string) (Unit, error) {
	use = strings.TrimSpace(use)
	if use == "" {
		return Unit{}, fmt.Errorf("unit kind is required")
	}
	if pluginName, kind, ok := strings.Cut(use, ":"); ok {
		for _, unit := range c.unitsByKind[kind] {
			if unit.PluginName == pluginName {
				return unit, nil
			}
		}
		return Unit{}, fmt.Errorf("unit kind %q from plugin %q is not registered", kind, pluginName)
	}
	matches := c.unitsByKind[use]
	switch len(matches) {
	case 0:
		return Unit{}, fmt.Errorf("unit kind %q is not registered", use)
	case 1:
		return matches[0], nil
	default:
		plugins := make([]string, 0, len(matches))
		for _, unit := range matches {
			plugins = append(plugins, unit.PluginName)
		}
		return Unit{}, fmt.Errorf("unit kind %q is ambiguous across plugins: %s", use, strings.Join(plugins, ", "))
	}
}
