// Package dataplane exposes the official pure-data wkbench plugin.
package dataplane

import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
	personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
	identitypool "github.com/WuKongIM/wkbench/units/identity/pool"
	assertunit "github.com/WuKongIM/wkbench/units/report/assert"
)

// Plugin returns the official pure-data unit plugin.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "wkbench.official.data",
		Version: "dev",
		Units: []contract.Unit{
			identitypool.Unit{},
			personpairs.Unit{},
			staticgroups.Unit{},
			assertunit.Unit{},
		},
	}
}
