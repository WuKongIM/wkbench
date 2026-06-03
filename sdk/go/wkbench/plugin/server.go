package plugin

import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
)

type Plugin struct {
	Name    string
	Version string
	Units   []contract.Unit
}

func ManifestFromUnits(name, version string, units []contract.Unit) pluginhost.Plugin {
	out := pluginhost.Plugin{Name: name, Version: version, Protocol: "wkbench.plugin/v1"}
	for _, unit := range units {
		def := unit.Definition()
		out.Units = append(out.Units, pluginhost.Unit{
			Kind:        def.Kind,
			Title:       def.Title,
			Description: def.Description,
			Inputs:      def.Inputs,
			Outputs:     def.Outputs,
			Metrics:     def.Metrics,
			Artifacts:   def.Artifacts,
		})
	}
	return out
}
