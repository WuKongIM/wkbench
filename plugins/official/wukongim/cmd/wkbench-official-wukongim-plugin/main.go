package main

import (
	"log"
	"os"

	officialwukongim "github.com/WuKongIM/wkbench/plugins/official/wukongim"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := plugin.Serve(officialwukongim.Plugin(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
