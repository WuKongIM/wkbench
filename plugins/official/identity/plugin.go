// Package identity exposes official identity data units as a wkbench plugin.
package identity

import (
	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
	identitypool "github.com/WuKongIM/wkbench/units/identity/pool"
)

// Plugin returns the official identity data unit plugin.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "wkbench.official.identity",
		Version: "dev",
		Units: []contract.Unit{
			identitypool.Unit{},
			personpairs.Unit{},
		},
	}
}
