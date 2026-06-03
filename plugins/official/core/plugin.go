// Package core exposes official core data units as a wkbench plugin.
package core

import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
)

// Plugin returns the official core data unit plugin.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "wkbench.official.core",
		Version: "dev",
		Units: []contract.Unit{
			staticgroups.Unit{},
		},
	}
}
