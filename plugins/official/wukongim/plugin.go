// Package wukongim exposes official WuKongIM setup units as a wkbench plugin.
package wukongim

import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	preparegroups "github.com/WuKongIM/wkbench/units/wukongim/prepare_group_channels"
	wukongtarget "github.com/WuKongIM/wkbench/units/wukongim/target"
)

// Plugin returns the official WuKongIM setup unit plugin.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "wkbench.official.wukongim",
		Version: "dev",
		Units: []contract.Unit{
			wukongtarget.Unit{},
			preparegroups.Unit{},
		},
	}
}
