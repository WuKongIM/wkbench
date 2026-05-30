package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/WuKongIM/wkbench/benchkit/dsl"
	"github.com/WuKongIM/wkbench/benchkit/kernel"
	"github.com/WuKongIM/wkbench/benchkit/registry"
	"github.com/WuKongIM/wkbench/benchkit/report"
	fakegroupsender "github.com/WuKongIM/wkbench/units/core/fake_group_sender"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
	assertunit "github.com/WuKongIM/wkbench/units/report/assert"
	groupsend "github.com/WuKongIM/wkbench/units/traffic/group_send"
)

const (
	exitOK       = 0
	exitConfig   = 1
	exitRun      = 2
	exitInternal = 3
)

func main() {
	os.Exit(runWithStderr(os.Args[1:], os.Stderr))
}

func runWithStderr(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench <list-units|validate|run>")
		return exitConfig
	}
	reg := defaultRegistry()
	switch args[0] {
	case "list-units":
		return runListUnits(reg, stderr)
	case "validate":
		return runValidate(reg, args[1:], stderr)
	case "run":
		return runScenario(reg, args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return exitConfig
	}
}

func defaultRegistry() *registry.Registry {
	reg := registry.New()
	staticgroups.Register(reg)
	fakegroupsender.Register(reg)
	reg.MustRegister(groupsend.Unit{})
	assertunit.Register(reg)
	return reg
}

func runListUnits(reg *registry.Registry, stderr io.Writer) int {
	for _, def := range reg.Definitions() {
		fmt.Fprintln(stderr, def.Kind)
	}
	return exitOK
}

func runValidate(reg *registry.Registry, args []string, stderr io.Writer) int {
	scenario, code := parseScenarioArg(args, stderr)
	if code != exitOK {
		return code
	}
	if err := kernel.New(reg).Validate(context.Background(), scenario); err != nil {
		fmt.Fprintf(stderr, "validate failed: %v\n", err)
		return exitConfig
	}
	fmt.Fprintln(stderr, "wkbench scenario is valid")
	return exitOK
}

func runScenario(reg *registry.Registry, args []string, stderr io.Writer) int {
	scenario, code := parseScenarioArg(args, stderr)
	if code != exitOK {
		return code
	}
	result, err := kernel.New(reg).Run(context.Background(), scenario)
	if err != nil {
		fmt.Fprintf(stderr, "run failed: %v\n", err)
		return exitRun
	}
	if scenario.Run.ReportDir != "" {
		if err := report.WriteDir(scenario.Run.ReportDir, result); err != nil {
			fmt.Fprintf(stderr, "report write failed: %v\n", err)
			return exitInternal
		}
	}
	fmt.Fprintln(stderr, "wkbench run completed")
	return exitOK
}

func parseScenarioArg(args []string, stderr io.Writer) (dsl.Scenario, int) {
	fs := flag.NewFlagSet("scenario", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var scenarioPath string
	fs.StringVar(&scenarioPath, "scenario", "", "path to wkbench/v2 scenario yaml")
	if err := fs.Parse(args); err != nil {
		return dsl.Scenario{}, exitConfig
	}
	if scenarioPath == "" {
		fmt.Fprintln(stderr, "-scenario is required")
		return dsl.Scenario{}, exitConfig
	}
	file, err := os.Open(scenarioPath)
	if err != nil {
		fmt.Fprintf(stderr, "open scenario failed: %v\n", err)
		return dsl.Scenario{}, exitConfig
	}
	defer file.Close()
	scenario, err := dsl.Parse(file)
	if err != nil {
		fmt.Fprintf(stderr, "parse scenario failed: %v\n", err)
		return dsl.Scenario{}, exitConfig
	}
	return scenario, exitOK
}
