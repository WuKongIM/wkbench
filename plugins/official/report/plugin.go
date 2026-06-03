// Package report exposes official report units as a wkbench plugin.
package report

import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	assertunit "github.com/WuKongIM/wkbench/units/report/assert"
)

// Plugin returns the official report unit plugin.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "wkbench.official.report",
		Version: "dev",
		Units: []contract.Unit{
			assertunit.Unit{},
		},
	}
}
