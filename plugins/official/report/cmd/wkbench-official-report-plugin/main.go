package main

import (
	"log"
	"os"

	officialreport "github.com/WuKongIM/wkbench/plugins/official/report"
	"github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
)

func main() {
	if err := plugin.Serve(officialreport.Plugin(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
