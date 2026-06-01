package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/scaffold"
)

func TestNewUnitCreatesStandardFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "units", "custom", "echo_unit")
	if err := scaffold.NewUnit(scaffold.UnitSpec{
		Kind:        "custom.echo/v1",
		Dir:         dir,
		Title:       "Echo unit",
		Description: "Echoes deterministic inputs for tests.",
	}); err != nil {
		t.Fatalf("new unit: %v", err)
	}

	unitGo := readFile(t, filepath.Join(dir, "unit.go"))
	unitTest := readFile(t, filepath.Join(dir, "unit_test.go"))
	readme := readFile(t, filepath.Join(dir, "README.md"))

	for _, want := range []string{
		"package echounit",
		`const kind = "custom.echo/v1"`,
		"func Register(reg *registry.Registry)",
		"func (Unit) Definition() contract.Definition",
		"func (Unit) Validate(ctx context.Context, env contract.ValidateEnv) error",
		"func (Unit) Plan(ctx context.Context, env contract.PlanEnv) (contract.Plan, error)",
		"func (Unit) Run(ctx context.Context, env contract.RunEnv) error",
	} {
		if !strings.Contains(unitGo, want) {
			t.Fatalf("unit.go missing %q:\n%s", want, unitGo)
		}
	}
	if !strings.Contains(unitTest, "unittest.AssertUnitContract(t, Unit{})") {
		t.Fatalf("unit_test.go missing contract assertion:\n%s", unitTest)
	}
	if !strings.Contains(readme, "custom.echo/v1") {
		t.Fatalf("README.md missing kind:\n%s", readme)
	}
}

func TestNewUnitRejectsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unit.go"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := scaffold.NewUnit(scaffold.UnitSpec{Kind: "custom.echo/v1", Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already exists error, got %v", err)
	}
}

func TestNewUnitRequiresVersionedKind(t *testing.T) {
	err := scaffold.NewUnit(scaffold.UnitSpec{Kind: "custom.echo", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "kind must end with /vN") {
		t.Fatalf("expected versioned kind error, got %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
