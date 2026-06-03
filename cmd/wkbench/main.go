package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/dsl"
	"github.com/WuKongIM/wkbench/benchkit/kernel"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
	"github.com/WuKongIM/wkbench/benchkit/registry"
	"github.com/WuKongIM/wkbench/benchkit/report"
	"github.com/WuKongIM/wkbench/benchkit/scaffold"
	fakegroupsender "github.com/WuKongIM/wkbench/units/core/fake_group_sender"
	fakemessagesender "github.com/WuKongIM/wkbench/units/core/fake_message_sender"
	staticgroups "github.com/WuKongIM/wkbench/units/core/static_groups"
	personpairs "github.com/WuKongIM/wkbench/units/identity/person_pairs"
	identitypool "github.com/WuKongIM/wkbench/units/identity/pool"
	assertunit "github.com/WuKongIM/wkbench/units/report/assert"
	groupsend "github.com/WuKongIM/wkbench/units/traffic/group_send"
	sendtraffic "github.com/WuKongIM/wkbench/units/traffic/send"
	sessionpool "github.com/WuKongIM/wkbench/units/wkproto/session_pool"
	metricscollector "github.com/WuKongIM/wkbench/units/wukongim/metrics_collector"
	preparegroups "github.com/WuKongIM/wkbench/units/wukongim/prepare_group_channels"
	preparetokens "github.com/WuKongIM/wkbench/units/wukongim/prepare_tokens"
	wukongtarget "github.com/WuKongIM/wkbench/units/wukongim/target"
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

type cliConfig struct {
	Plugins []string
	Command string
	Args    []string
}

func parseGlobalArgs(args []string, stderr io.Writer) (cliConfig, int) {
	fs := flag.NewFlagSet("wkbench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var plugins multiString
	fs.Var(&plugins, "plugin", "external wkbench plugin executable; may be repeated")
	if err := fs.Parse(args); err != nil {
		return cliConfig{}, exitConfig
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench [-plugin path] <list-units|new-unit|explain|plan|validate|run>")
		return cliConfig{}, exitConfig
	}
	return cliConfig{Plugins: plugins, Command: rest[0], Args: rest[1:]}, exitOK
}

type multiString []string

func (m *multiString) String() string {
	return strings.Join(*m, ",")
}

func (m *multiString) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func runWithStderr(args []string, stderr io.Writer) int {
	cfg, code := parseGlobalArgs(args, stderr)
	if code != exitOK {
		return code
	}
	reg := defaultRegistry()
	clients, code := loadExternalPlugins(reg, cfg.Plugins, stderr)
	if code != exitOK {
		return code
	}
	defer closePluginClients(clients, stderr)
	switch cfg.Command {
	case "list-units":
		return runListUnits(reg, stderr)
	case "new-unit":
		return runNewUnit(cfg.Args, stderr)
	case "explain":
		return runExplain(reg, cfg.Args, stderr)
	case "plan":
		return runPlan(reg, cfg.Args, stderr)
	case "validate":
		return runValidate(reg, cfg.Args, stderr)
	case "run":
		return runScenario(reg, cfg.Args, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", cfg.Command)
		return exitConfig
	}
}

func defaultRegistry() *registry.Registry {
	reg := registry.New()
	staticgroups.Register(reg)
	fakegroupsender.Register(reg)
	fakemessagesender.Register(reg)
	identitypool.Register(reg)
	personpairs.Register(reg)
	wukongtarget.Register(reg)
	metricscollector.Register(reg)
	preparetokens.Register(reg)
	preparegroups.Register(reg)
	sessionpool.Register(reg)
	reg.MustRegister(groupsend.Unit{})
	sendtraffic.Register(reg)
	assertunit.Register(reg)
	return reg
}

func loadExternalPlugins(reg *registry.Registry, paths []string, stderr io.Writer) ([]*pluginhost.StdioClient, int) {
	type loadedPlugin struct {
		path     string
		client   *pluginhost.StdioClient
		manifest pluginhost.Plugin
	}
	var clients []*pluginhost.StdioClient
	var loaded []loadedPlugin
	bareKindCounts := make(map[string]int)
	for _, path := range paths {
		client, err := pluginhost.StartStdioClient(context.Background(), path)
		if err != nil {
			fmt.Fprintf(stderr, "plugin %s failed to start: %v\n", path, err)
			closePluginClients(clients, stderr)
			return nil, exitConfig
		}
		clients = append(clients, client)
		manifest, err := client.Handshake(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "plugin %s handshake failed: %v\n", path, err)
			closePluginClients(clients, stderr)
			return nil, exitConfig
		}
		loaded = append(loaded, loadedPlugin{path: path, client: client, manifest: manifest})
		for _, unit := range manifest.Units {
			bareKindCounts[unit.Kind]++
		}
	}
	for _, plugin := range loaded {
		remoteClient := pendingLifecycleClient{client: plugin.client}
		for _, unit := range plugin.manifest.Units {
			qualifiedKind := plugin.manifest.Name + ":" + unit.Kind
			if err := reg.Register(pluginhost.NewRemoteUnitAlias(remoteClient, unit, qualifiedKind)); err != nil {
				fmt.Fprintf(stderr, "plugin %s registration failed: %v\n", plugin.path, err)
				closePluginClients(clients, stderr)
				return nil, exitConfig
			}
			if bareKindCounts[unit.Kind] != 1 {
				continue
			}
			if err := reg.Register(pluginhost.NewRemoteUnit(remoteClient, unit)); err != nil {
				continue
			}
		}
	}
	return clients, exitOK
}

func closePluginClients(clients []*pluginhost.StdioClient, stderr io.Writer) {
	for i := len(clients) - 1; i >= 0; i-- {
		if err := clients[i].Close(); err != nil {
			fmt.Fprintf(stderr, "plugin close failed: %v\n", err)
		}
	}
}

type pendingLifecycleClient struct {
	client *pluginhost.StdioClient
}

func (c pendingLifecycleClient) Validate(ctx context.Context, req pluginhost.UnitRequest) error {
	return c.client.Validate(ctx, req)
}

func (c pendingLifecycleClient) Plan(ctx context.Context, req pluginhost.UnitRequest) (contract.Plan, error) {
	return c.client.Plan(ctx, req)
}

func (c pendingLifecycleClient) Run(ctx context.Context, req pluginhost.RunRequest, env contract.RunEnv) error {
	return c.client.Run(ctx, req, env)
}

func runListUnits(reg *registry.Registry, stderr io.Writer) int {
	for _, def := range reg.Definitions() {
		fmt.Fprintln(stderr, def.Kind)
	}
	return exitOK
}

func runNewUnit(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("new-unit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var spec scaffold.UnitSpec
	fs.StringVar(&spec.Kind, "kind", "", "versioned unit kind, for example demo.echo/v1")
	fs.StringVar(&spec.Dir, "dir", "", "target unit package directory")
	fs.StringVar(&spec.PackageName, "package", "", "optional Go package name")
	fs.StringVar(&spec.Title, "title", "", "optional human-readable unit title")
	fs.StringVar(&spec.Description, "description", "", "optional unit description")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if err := scaffold.NewUnit(spec); err != nil {
		fmt.Fprintf(stderr, "new-unit failed: %v\n", err)
		return exitConfig
	}
	fmt.Fprintf(stderr, "created unit %s in %s\n", spec.Kind, spec.Dir)
	return exitOK
}

func runExplain(reg *registry.Registry, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var scenarioPath string
	var format string
	fs.StringVar(&scenarioPath, "scenario", "", "path to wkbench/v2 scenario yaml")
	fs.StringVar(&format, "format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if scenarioPath == "" {
		fmt.Fprintln(stderr, "-scenario is required")
		return exitConfig
	}
	if format != "text" && format != "json" {
		fmt.Fprintf(stderr, "unsupported explain format %q\n", format)
		return exitConfig
	}
	scenario, code := loadScenario(scenarioPath, stderr)
	if code != exitOK {
		return code
	}
	explanation, err := kernel.New(reg).Explain(context.Background(), scenario)
	if err != nil {
		fmt.Fprintf(stderr, "explain failed: %v\n", err)
		return exitConfig
	}
	switch format {
	case "json":
		data, err := json.MarshalIndent(explanation, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "marshal explanation failed: %v\n", err)
			return exitInternal
		}
		fmt.Fprintln(stderr, string(data))
	default:
		writeExplainText(stderr, explanation)
	}
	return exitOK
}

func runPlan(reg *registry.Registry, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var scenarioPath string
	var format string
	fs.StringVar(&scenarioPath, "scenario", "", "path to wkbench/v2 scenario yaml")
	fs.StringVar(&format, "format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if scenarioPath == "" {
		fmt.Fprintln(stderr, "-scenario is required")
		return exitConfig
	}
	if format != "text" && format != "json" {
		fmt.Fprintf(stderr, "unsupported plan format %q\n", format)
		return exitConfig
	}
	scenario, code := loadScenario(scenarioPath, stderr)
	if code != exitOK {
		return code
	}
	result, err := kernel.New(reg).Plan(context.Background(), scenario)
	if err != nil {
		fmt.Fprintf(stderr, "plan failed: %v\n", err)
		return exitConfig
	}
	switch format {
	case "json":
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "marshal plan failed: %v\n", err)
			return exitInternal
		}
		fmt.Fprintln(stderr, string(data))
	default:
		writePlanText(stderr, result)
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
	if scenario.Run.ReportDir != "" {
		if err := report.WriteDir(scenario.Run.ReportDir, result); err != nil {
			fmt.Fprintf(stderr, "report write failed: %v\n", err)
			return exitInternal
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "run failed: %v\n", err)
		return exitRun
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
	return loadScenario(scenarioPath, stderr)
}

func loadScenario(scenarioPath string, stderr io.Writer) (dsl.Scenario, int) {
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

func writeExplainText(w io.Writer, explanation kernel.Explanation) {
	fmt.Fprintf(w, "Run: %s\n", explanation.RunID)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Execution Order:")
	for i, name := range explanation.Order {
		unit := explanation.Units[name]
		if unit.Kind == "" {
			fmt.Fprintf(w, "  %d. %s\n", i+1, name)
			continue
		}
		fmt.Fprintf(w, "  %d. %s (%s)\n", i+1, name, unit.Kind)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Units:")
	for _, name := range explanation.Order {
		unit := explanation.Units[name]
		fmt.Fprintf(w, "  %s: %s\n", name, unit.Kind)
		if len(unit.After) > 0 {
			fmt.Fprintf(w, "    after: %v\n", unit.After)
		}
		writeExplainPorts(w, "inputs", unit.Inputs)
		writeExplainPorts(w, "outputs", unit.Outputs)
	}
	fmt.Fprintln(w)
	writeWiringText(w, explanation.Wiring)
}

func writePlanText(w io.Writer, result kernel.PlanResult) {
	fmt.Fprintf(w, "Run: %s\n", result.RunID)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Execution Order:")
	for i, name := range result.Order {
		unit := result.Units[name]
		if unit.Kind == "" {
			fmt.Fprintf(w, "  %d. %s\n", i+1, name)
			continue
		}
		fmt.Fprintf(w, "  %d. %s (%s)\n", i+1, name, unit.Kind)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Plans:")
	for _, name := range result.Order {
		unit := result.Units[name]
		fmt.Fprintf(w, "  %s: %s\n", name, unit.Kind)
		fmt.Fprintf(w, "    status: %s\n", unit.Status)
		if unit.Error != "" {
			fmt.Fprintf(w, "    error: %s\n", unit.Error)
		}
		if len(unit.Plan.Shards) > 0 {
			fmt.Fprintf(w, "    shards: %d\n", len(unit.Plan.Shards))
		}
	}
	fmt.Fprintln(w)
	writeWiringText(w, result.Wiring)
}

func writeWiringText(w io.Writer, wiring []kernel.ExplainBinding) {
	fmt.Fprintln(w, "Wiring:")
	if len(wiring) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, binding := range wiring {
		fmt.Fprintf(w, "  %s.%s <- %s.%s (%s)\n",
			binding.Unit,
			binding.Input,
			binding.SourceUnit,
			binding.SourceOutput,
			binding.Type,
		)
	}
}

func writeExplainPorts(w io.Writer, title string, ports []kernel.ExplainPort) {
	if len(ports) == 0 {
		return
	}
	fmt.Fprintf(w, "    %s:\n", title)
	for _, port := range ports {
		optional := ""
		if port.Optional {
			optional = " optional"
		}
		fmt.Fprintf(w, "      %s %s%s\n", port.Name, port.Type, optional)
	}
}
