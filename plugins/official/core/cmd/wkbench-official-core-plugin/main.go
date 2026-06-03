package main

import (
	"log"
	"os"

	officialcore "github.com/WuKongIM/wkbench/plugins/official/core"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := plugin.Serve(officialcore.Plugin(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
