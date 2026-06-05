package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"unicode"
)

type pluginTemplateSpec struct {
	Dir          string
	Module       string
	PluginName   string
	CommandName  string
	UnitKind     string
	UnitConst    string
	WKBenchRoot  string
	ScenarioID   string
	QualifiedUse string
}

type pluginTemplateFile struct {
	Template string
	Mode     fs.FileMode
}

func runPluginInit(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var spec pluginTemplateSpec
	fs.StringVar(&spec.Dir, "dir", "", "target plugin project directory")
	fs.StringVar(&spec.Module, "module", "", "Go module path for the plugin project")
	fs.StringVar(&spec.PluginName, "name", "", "wkbench plugin name")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if spec.Dir == "" || spec.Module == "" || spec.PluginName == "" {
		fmt.Fprintln(stderr, "plugin init requires -dir, -module, and -name")
		return exitConfig
	}
	if err := initPluginTemplate(&spec); err != nil {
		fmt.Fprintf(stderr, "plugin init failed: %v\n", err)
		return exitConfig
	}
	fmt.Fprintf(stderr, "created plugin template %s in %s\n", spec.PluginName, spec.Dir)
	return exitOK
}

func initPluginTemplate(spec *pluginTemplateSpec) error {
	root, err := findWKBenchRoot()
	if err != nil {
		return err
	}
	spec.WKBenchRoot = root
	spec.PluginName = strings.TrimSpace(spec.PluginName)
	spec.Module = strings.TrimSpace(spec.Module)
	spec.Dir = filepath.Clean(spec.Dir)
	spec.CommandName = commandNameFromPlugin(spec.PluginName)
	spec.UnitKind = unitKindFromPlugin(spec.PluginName)
	spec.UnitConst = spec.UnitKind
	spec.ScenarioID = strings.ReplaceAll(spec.PluginName, ".", "-") + "-demo"
	spec.QualifiedUse = spec.PluginName + ":" + spec.UnitKind

	files := map[string]pluginTemplateFile{
		"go.mod":     {Template: pluginGoModTemplate, Mode: 0o644},
		"go.sum":     {Template: pluginGoSumTemplate, Mode: 0o644},
		".gitignore": {Template: pluginGitignoreTemplate, Mode: 0o644},
		filepath.Join("cmd", spec.CommandName, "main.go"): {Template: pluginMainTemplate, Mode: 0o644},
		filepath.Join("units", "echo", "unit.go"):         {Template: pluginUnitTemplate, Mode: 0o644},
		filepath.Join("units", "echo", "unit_test.go"):    {Template: pluginUnitTestTemplate, Mode: 0o644},
		filepath.Join("examples", "echo.yaml"):            {Template: pluginScenarioTemplate, Mode: 0o644},
		filepath.Join("scripts", "check.sh"):              {Template: pluginCheckScriptTemplate, Mode: 0o755},
		"README.md":                                       {Template: pluginReadmeTemplate, Mode: 0o644},
	}
	for name := range files {
		path := filepath.Join(spec.Dir, name)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	for name, entry := range files {
		path := filepath.Join(spec.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		data, err := renderPluginTemplate(name, entry.Template, spec)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, entry.Mode); err != nil {
			return err
		}
	}
	return nil
}

func renderPluginTemplate(name, text string, spec *pluginTemplateSpec) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, spec); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func findWKBenchRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, err := findWKBenchRootFrom(dir); err == nil {
		return root, nil
	}
	_, file, _, ok := runtime.Caller(0)
	if ok {
		if root, err := findWKBenchRootFrom(filepath.Dir(file)); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("wkbench module root not found")
}

func findWKBenchRootFrom(dir string) (string, error) {
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/WuKongIM/wkbench") {
			return dir, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("wkbench module root not found")
		}
		dir = parent
	}
}

func commandNameFromPlugin(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "wkbench"
	}
	return out + "-plugin"
}

func unitKindFromPlugin(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "demo.echo/v1"
	}
	return name + "/v1"
}

const pluginGoModTemplate = `module {{.Module}}

go 1.23.0

require (
	github.com/WuKongIM/wkbench v0.0.0
	google.golang.org/protobuf v1.36.6
)

replace github.com/WuKongIM/wkbench => {{.WKBenchRoot}}
`

const pluginGoSumTemplate = `google.golang.org/protobuf v1.36.6 h1:z1NpPI8ku2WgiWnf+t9wTPsn6eP1L7ksHUlkfLvd9xY=
google.golang.org/protobuf v1.36.6/go.mod h1:jduwjTPXsFjZGTmRluh+L6NjiWu7pchiJ2/5YcXBHnY=
`

const pluginGitignoreTemplate = `bin/
reports/
`

const pluginMainTemplate = `package main

import (
	"os"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkplugin "github.com/WuKongIM/wkbench/sdk/go/wkbench/plugin"
	"{{.Module}}/units/echo"
)

func main() {
	if err := wkplugin.Serve(wkplugin.Plugin{
		Name:    "{{.PluginName}}",
		Version: "0.1.0",
		Units:   []contract.Unit{echo.Unit{}},
	}, os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
`

const pluginUnitTemplate = `package echo

import (
	"context"
	"encoding/json"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

const kind = "{{.UnitConst}}"

type Unit struct{}

type Spec struct {
	Message string ` + "`json:\"message\"`" + `
}

type Result struct {
	Message string ` + "`json:\"message\"`" + `
}

func (r Result) ReportOutput() any {
	return r
}

func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       "Echo",
		Description: "Echoes a message through an external wkbench plugin.",
		Outputs: []contract.PortDef{
			{
				Name: "result",
				Type: "port.demo.echo/v1",
				Meta: contract.PortMeta{
					Boundary:   contract.PortBoundaryData,
					Transport:  contract.PortTransportInline,
					Reportable: true,
				},
			},
		},
		Artifacts: []contract.ArtifactDef{
			{
				Name:        "echo.json",
				ContentType: "application/json",
			},
		},
	}
}

func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	var spec Spec
	return env.DecodeSpec(&spec)
}

func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return err
	}
	result := Result{Message: spec.Message}
	if err := env.SetOutput("result", result); err != nil {
		return err
	}
	artifact, err := env.OpenArtifact("echo.json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(artifact).Encode(result); err != nil {
		_ = artifact.Close()
		return err
	}
	return artifact.Close()
}
`

const pluginUnitTestTemplate = `package echo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func TestUnitRunWritesOutputAndArtifact(t *testing.T) {
	reportDir := filepath.Join(t.TempDir(), "report")
	env := contract.NewTestRunEnv("run-1", "echo", nil, map[string]any{
		"message": "hello from test",
	})
	env.DeclareArtifacts((Unit{}).Definition().Artifacts)
	env.SetReportDir(reportDir)

	if err := (Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	output, ok := env.Output("result")
	if !ok {
		t.Fatal("result output not set")
	}
	result, ok := output.(Result)
	if !ok || result.Message != "hello from test" {
		t.Fatalf("unexpected result: %#v", output)
	}
	if _, err := os.Stat(filepath.Join(reportDir, "artifacts", "echo", "echo.json")); err != nil {
		t.Fatalf("artifact not written: %v", err)
	}
}
`

const pluginScenarioTemplate = `version: wkbench/v2

run:
  id: {{.ScenarioID}}
  duration: 1s
  report_dir: ./reports/{{.ScenarioID}}

units:
  echo:
    use: {{.QualifiedUse}}
    spec:
      message: hello from external plugin
`

const pluginCheckScriptTemplate = `#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
: "${WKBENCH:=wkbench}"
: "${GOWORK:=off}"
export GOWORK

go test ./...
mkdir -p ./bin
go build -o ./bin/{{.CommandName}} ./cmd/{{.CommandName}}
"$WKBENCH" plugin check ./bin/{{.CommandName}} -scenario ./examples/echo.yaml
`

const pluginReadmeTemplate = `# {{.PluginName}}

This is an external wkbench plugin generated by ` + "`wkbench plugin init`" + `.

## Local Check

Run the generated check script before sharing changes:

` + "```bash" + `
./scripts/check.sh
WKBENCH=/path/to/wkbench ./scripts/check.sh
` + "```" + `

## Build

` + "```bash" + `
go test ./...
go build -o ./bin/{{.CommandName}} ./cmd/{{.CommandName}}
wkbench plugin check ./bin/{{.CommandName}} -scenario ./examples/echo.yaml
` + "```" + `

## Use With wkbench

` + "```bash" + `
wkbench plugin add {{.PluginName}} ./bin/{{.CommandName}}
wkbench plugin doctor
wkbench validate -scenario ./examples/echo.yaml
wkbench explain -scenario ./examples/echo.yaml
wkbench plan -scenario ./examples/echo.yaml
wkbench run -scenario ./examples/echo.yaml
` + "```" + `

Scenarios should use the plugin-qualified unit kind:

` + "```yaml" + `
version: wkbench/v2
run:
  id: {{.ScenarioID}}
  duration: 1s
units:
  echo:
    use: {{.QualifiedUse}}
    spec:
      message: hello from external plugin
` + "```" + `

## Phase 1 Port Limits

Phase 1 plugin ports are for non-sensitive inline JSON data. Keep report outputs
JSON-friendly and do not expose tokens, clients, file handles, or secrets. Write
large raw samples and bulky outputs as artifacts instead of inline port data.

## Release Checklist

- ` + "`./scripts/check.sh`" + ` passes.
- Build and name the binary for the version you are releasing.
- Consumers know to use ` + "`{{.QualifiedUse}}`" + ` in scenarios.
- Example scenarios validate with ` + "`wkbench validate`" + ` and ` + "`wkbench plugin check`" + `.
`
