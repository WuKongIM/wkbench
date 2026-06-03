package main

import (
	"log"
	"os"

	officialdata "github.com/WuKongIM/wkbench/plugins/official/dataplane"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := plugin.Serve(officialdata.Plugin(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
