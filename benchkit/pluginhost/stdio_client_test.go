package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStdioClientListsDemoPluginUnits(t *testing.T) {
	bin := buildDemoPlugin(t)

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	manifest, err := client.Handshake(context.Background())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if manifest.Name != "wkbench.demo" {
		t.Fatalf("Name = %q", manifest.Name)
	}
	if len(manifest.Units) != 1 || manifest.Units[0].Kind != "demo.echo/v1" {
		t.Fatalf("units = %#v", manifest.Units)
	}
}

func TestStdioClientHandshakeIgnoresCanceledContext(t *testing.T) {
	bin := buildDemoPlugin(t)

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manifest, err := client.Handshake(ctx)
	if err != nil {
		t.Fatalf("handshake with canceled context: %v", err)
	}
	if manifest.Name != "wkbench.demo" {
		t.Fatalf("Name = %q", manifest.Name)
	}
}

func buildDemoPlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "wkbench-demo-plugin")
	build := exec.Command("go", "build", "-o", bin, "./plugins/demo/cmd/wkbench-demo-plugin")
	build.Env = append(os.Environ(), "GOWORK=off")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}
