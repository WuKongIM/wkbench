package pluginhost

import (
	"slices"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

type Plugin struct {
	Name     string
	Version  string
	Protocol string
	Source   string
	Checksum string
	Units    []Unit
}

type Unit struct {
	PluginName  string
	Kind        string
	Title       string
	Description string
	Inputs      []contract.PortDef
	Outputs     []contract.PortDef
	Metrics     []contract.MetricDef
	Artifacts   []contract.ArtifactDef
	Background  bool
}

func (u Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        u.Kind,
		Title:       u.Title,
		Description: u.Description,
		Inputs:      clonePortDefs(u.Inputs),
		Outputs:     clonePortDefs(u.Outputs),
		Metrics:     slices.Clone(u.Metrics),
		Artifacts:   slices.Clone(u.Artifacts),
	}
}

func cloneUnit(u Unit) Unit {
	u.Inputs = clonePortDefs(u.Inputs)
	u.Outputs = clonePortDefs(u.Outputs)
	u.Metrics = slices.Clone(u.Metrics)
	u.Artifacts = slices.Clone(u.Artifacts)
	return u
}

func clonePortDefs(ports []contract.PortDef) []contract.PortDef {
	ports = slices.Clone(ports)
	for i := range ports {
		ports[i].Meta.Encodings = slices.Clone(ports[i].Meta.Encodings)
		ports[i].Meta.Operations = slices.Clone(ports[i].Meta.Operations)
	}
	return ports
}
