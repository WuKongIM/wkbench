package pluginhost

import "github.com/WuKongIM/wkbench/benchkit/contract"

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
}

func (u Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        u.Kind,
		Title:       u.Title,
		Description: u.Description,
		Inputs:      u.Inputs,
		Outputs:     u.Outputs,
		Metrics:     u.Metrics,
		Artifacts:   u.Artifacts,
	}
}
