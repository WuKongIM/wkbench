// Package scaffold generates wkbench authoring boilerplate.
package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

// UnitSpec describes one unit scaffold to generate.
type UnitSpec struct {
	// Kind is the versioned unit kind, for example demo.echo/v1.
	Kind string
	// Dir is the target package directory.
	Dir string
	// PackageName overrides the generated Go package name.
	PackageName string
	// Title is the human-readable unit title.
	Title string
	// Description explains what the unit does.
	Description string
}

// NewUnit creates standard unit.go, unit_test.go, and README.md files.
func NewUnit(spec UnitSpec) error {
	spec = normalizeSpec(spec)
	if !hasVersionSuffix(spec.Kind) {
		return fmt.Errorf("kind must end with /vN, got %q", spec.Kind)
	}
	if spec.Dir == "" {
		return fmt.Errorf("dir is required")
	}
	files := map[string]string{
		"unit.go":      unitTemplate,
		"unit_test.go": unitTestTemplate,
		"README.md":    readmeTemplate,
	}
	for name := range files {
		path := filepath.Join(spec.Dir, name)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(spec.Dir, 0o755); err != nil {
		return err
	}
	for name, source := range files {
		rendered, err := render(source, spec)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(spec.Dir, name), rendered, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func normalizeSpec(spec UnitSpec) UnitSpec {
	spec.Kind = strings.TrimSpace(spec.Kind)
	spec.Dir = strings.TrimSpace(spec.Dir)
	spec.PackageName = sanitizePackageName(spec.PackageName)
	if spec.PackageName == "" {
		spec.PackageName = sanitizePackageName(filepath.Base(spec.Dir))
	}
	if spec.PackageName == "" {
		spec.PackageName = "unit"
	}
	spec.Title = strings.TrimSpace(spec.Title)
	if spec.Title == "" {
		spec.Title = titleFromKind(spec.Kind)
	}
	spec.Description = strings.TrimSpace(spec.Description)
	if spec.Description == "" {
		spec.Description = fmt.Sprintf("Implements the %s benchmark unit.", spec.Kind)
	}
	return spec
}

func sanitizePackageName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return ""
	}
	first, _ := utf8FirstRune(out)
	if !unicode.IsLetter(first) {
		out = "unit" + out
	}
	return out
}

func titleFromKind(kind string) string {
	base := strings.TrimSpace(kind)
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[:slash]
	}
	replacer := strings.NewReplacer(".", " ", "_", " ", "-", " ")
	words := strings.Fields(replacer.Replace(base))
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	if len(words) == 0 {
		return "Benchmark unit"
	}
	return strings.Join(words, " ")
}

func hasVersionSuffix(kind string) bool {
	idx := strings.LastIndex(kind, "/v")
	if idx < 0 || idx+2 >= len(kind) {
		return false
	}
	for _, r := range kind[idx+2:] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func utf8FirstRune(value string) (rune, bool) {
	for _, r := range value {
		return r, true
	}
	return 0, false
}

func render(source string, spec UnitSpec) ([]byte, error) {
	tmpl, err := template.New("unit").Funcs(template.FuncMap{
		"quote": strconv.Quote,
	}).Parse(source)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, spec); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const unitTemplate = `// Package {{.PackageName}} implements {{.Kind}}.
package {{.PackageName}}

import (
	"context"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/registry"
)

const kind = {{quote .Kind}}

// Unit implements {{.Kind}}.
type Unit struct{}

// Spec configures {{.Kind}}.
type Spec struct{}

// Register adds this unit to reg.
func Register(reg *registry.Registry) {
	reg.MustRegister(Unit{})
}

// Definition implements contract.Unit.
func (Unit) Definition() contract.Definition {
	return contract.Definition{
		Kind:        kind,
		Title:       {{quote .Title}},
		Description: {{quote .Description}},
	}
}

// Validate implements contract.Unit.
func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return err
	}
	return nil
}

// Plan implements contract.Unit.
func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error) {
	return contract.Plan{UnitName: env.UnitName()}, nil
}

// Run implements contract.Unit.
func (Unit) Run(ctx context.Context, env contract.RunEnv) error {
	var spec Spec
	if err := env.DecodeSpec(&spec); err != nil {
		return err
	}
	return nil
}
`

const unitTestTemplate = `package {{.PackageName}}

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, Unit{})
}

func TestValidateAcceptsEmptySpec(t *testing.T) {
	env := contract.NewTestRunEnv("run", "unit", nil, nil)
	if err := Unit{}.Validate(context.Background(), env); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
`

const readmeTemplate = `# {{.Kind}}

{{.Description}}

## Contract

- Kind: ` + "`{{.Kind}}`" + `
- Package: ` + "`{{.PackageName}}`" + `

Register this unit in your distribution binary:

` + "```go" + `
{{.PackageName}}.Register(reg)
` + "```" + `

Compose it from scenario YAML:

` + "```yaml" + `
units:
  example:
    use: {{.Kind}}
    spec: {}
` + "```" + `
`
