package pluginhost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
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

func TestStdioClientCanceledHandshakeStopsHungPlugin(t *testing.T) {
	bin := writeSleepPlugin(t, 2*time.Second)

	client, err := StartStdioClient(context.Background(), bin)
	if err != nil {
		t.Fatalf("start client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = client.Handshake(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("handshake returned after %s, want prompt cancellation", elapsed)
	}

	closeStart := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	if elapsed := time.Since(closeStart); elapsed > time.Second {
		t.Fatalf("close returned after %s, want prompt close", elapsed)
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

func writeSleepPlugin(t *testing.T, delay time.Duration) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wkbench-sleep-plugin")
	seconds := int(delay / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	body := "#!/bin/sh\nsleep " + strconv.Itoa(seconds) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write sleep plugin: %v", err)
	}
	return path
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
