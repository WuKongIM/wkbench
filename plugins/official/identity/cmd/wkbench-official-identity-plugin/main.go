package main

import (
	"log"
	"os"

	officialidentity "github.com/WuKongIM/wkbench/plugins/official/identity"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := plugin.Serve(officialidentity.Plugin(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
