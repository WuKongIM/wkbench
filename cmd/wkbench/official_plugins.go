package main

import (
	"fmt"
	"io"
	"os"

	officialcore "github.com/WuKongIM/wkbench/plugins/official/core"
	officialidentity "github.com/WuKongIM/wkbench/plugins/official/identity"
	officialreport "github.com/WuKongIM/wkbench/plugins/official/report"
	officialwukongim "github.com/WuKongIM/wkbench/plugins/official/wukongim"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

const officialPluginCommand = "__official-plugin"

var officialPluginSpecsForTest func() ([]pluginCommandSpec, error)

func defaultOfficialPluginSpecs() ([]pluginCommandSpec, error) {
	if officialPluginSpecsForTest != nil {
		return officialPluginSpecsForTest()
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []pluginCommandSpec{
		{Label: "wkbench.official.core", Path: exe, Args: []string{officialPluginCommand, "core"}},
		{Label: "wkbench.official.identity", Path: exe, Args: []string{officialPluginCommand, "identity"}},
		{Label: "wkbench.official.wukongim", Path: exe, Args: []string{officialPluginCommand, "wukongim"}},
		{Label: "wkbench.official.report", Path: exe, Args: []string{officialPluginCommand, "report"}},
	}, nil
}

func runOfficialPluginServe(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: wkbench __official-plugin <core|identity|wukongim|report>")
		return exitConfig
	}
	var manifest wkplugin.Plugin
	switch args[0] {
	case "core":
		manifest = officialcore.Plugin()
	case "identity":
		manifest = officialidentity.Plugin()
	case "wukongim":
		manifest = officialwukongim.Plugin()
	case "report":
		manifest = officialreport.Plugin()
	default:
		fmt.Fprintf(stderr, "unknown official plugin %q\n", args[0])
		return exitConfig
	}
	if err := wkplugin.Serve(manifest, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "official plugin %s failed: %v\n", manifest.Name, err)
		return exitInternal
	}
	return exitOK
}
