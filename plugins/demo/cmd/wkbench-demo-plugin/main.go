package main

import (
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/plugins/demo/echo"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name:    "wkbench.demo",
		Version: "0.1.0",
		Units:   []contract.Unit{echo.Unit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
