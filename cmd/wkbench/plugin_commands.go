package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	"github.com/WuKongIM/wkbench/benchkit/pluginhost"
)

const pluginManifestTimeout = 2 * time.Second

func runPluginCommand(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench plugin <add|list|doctor|inspect|init|check>")
		return exitConfig
	}
	switch args[0] {
	case "add":
		return runPluginAdd(args[1:], stderr)
	case "list":
		return runPluginList(args[1:], stderr)
	case "doctor":
		return runPluginDoctor(args[1:], stderr)
	case "inspect":
		return runPluginInspect(args[1:], stderr)
	case "init":
		return runPluginInit(args[1:], stderr)
	case "check":
		return runPluginCheck(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "unknown plugin command %q\n", args[0])
		return exitConfig
	}
}

func runPluginList(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	configPath, cfg, ok, err := readProjectPluginConfig()
	if err != nil {
		fmt.Fprintf(stderr, "plugin list failed: %v\n", err)
		return exitConfig
	}
	if !ok || len(cfg.Plugins) == 0 {
		fmt.Fprintln(stderr, "no plugins configured")
		return exitOK
	}
	fmt.Fprintf(stderr, "Config: %s\n", configPath)
	for _, plugin := range cfg.Plugins {
		status := "enabled"
		if !plugin.isEnabled() {
			status = "disabled"
		}
		fmt.Fprintf(stderr, "%s\t%s\t%s\n", plugin.Name, status, plugin.Path)
	}
	return exitOK
}

func runPluginAdd(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintln(stderr, "usage: wkbench plugin add <name> <path>")
		return exitConfig
	}
	name, pluginPath := rest[0], rest[1]
	if name == "" || pluginPath == "" {
		fmt.Fprintln(stderr, "plugin name and path are required")
		return exitConfig
	}
	configPath, cfg, err := pluginConfigPathForWrite()
	if err != nil {
		fmt.Fprintf(stderr, "plugin add failed: %v\n", err)
		return exitConfig
	}
	storedPath, err := normalizePluginPathForConfig(configPath, pluginPath)
	if err != nil {
		fmt.Fprintf(stderr, "plugin add failed: %v\n", err)
		return exitConfig
	}
	enabled := true
	entry := pluginConfigEntry{Name: name, Path: storedPath, Enabled: &enabled}
	replaced := false
	for i := range cfg.Plugins {
		if cfg.Plugins[i].Name == name {
			cfg.Plugins[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Plugins = append(cfg.Plugins, entry)
	}
	if err := writePluginConfigFile(configPath, cfg); err != nil {
		fmt.Fprintf(stderr, "plugin add failed: %v\n", err)
		return exitConfig
	}
	fmt.Fprintf(stderr, "configured plugin %s in %s\n", name, configPath)
	return exitOK
}

func runPluginDoctor(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	configPath, cfg, ok, err := readProjectPluginConfig()
	if err != nil {
		fmt.Fprintf(stderr, "plugin doctor failed: %v\n", err)
		return exitConfig
	}
	if !ok || len(cfg.Plugins) == 0 {
		fmt.Fprintln(stderr, "no plugins configured")
		return exitOK
	}
	projectDir := pluginConfigProjectDir(configPath)
	code := exitOK
	for _, plugin := range cfg.Plugins {
		if !plugin.isEnabled() {
			fmt.Fprintf(stderr, "%s skipped disabled path=%s\n", plugin.Name, plugin.Path)
			continue
		}
		path := resolvePluginPath(projectDir, plugin.Path)
		manifest, err := inspectPluginManifest(path)
		if err != nil {
			fmt.Fprintf(stderr, "%s failed path=%s error=%v\n", plugin.Name, plugin.Path, err)
			code = exitConfig
			continue
		}
		fmt.Fprintf(stderr, "%s ok plugin=%s version=%s protocol=%s path=%s\n", plugin.Name, manifest.Name, manifest.Version, manifest.Protocol, plugin.Path)
		for _, unit := range manifest.Units {
			fmt.Fprintf(stderr, "  unit %s\n", unit.Kind)
		}
	}
	return code
}

func runPluginInspect(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "usage: wkbench plugin inspect <name-or-path>")
		return exitConfig
	}
	path, err := resolvePluginInspectTarget(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "plugin inspect failed: %v\n", err)
		return exitConfig
	}
	manifest, err := inspectPluginManifest(path)
	if err != nil {
		fmt.Fprintf(stderr, "plugin inspect failed: %v\n", err)
		return exitConfig
	}
	writePluginManifest(stderr, manifest)
	return exitOK
}

func resolvePluginInspectTarget(target string) (string, error) {
	configPath, cfg, ok, err := readProjectPluginConfig()
	if err != nil || !ok {
		return target, err
	}
	projectDir := pluginConfigProjectDir(configPath)
	for _, plugin := range cfg.Plugins {
		if plugin.Name == target {
			return resolvePluginPath(projectDir, plugin.Path), nil
		}
	}
	return filepath.Clean(target), nil
}

func inspectPluginManifest(path string) (pluginhost.Plugin, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginManifestTimeout)
	defer cancel()
	client, err := pluginhost.StartStdioClient(ctx, path)
	if err != nil {
		return pluginhost.Plugin{}, err
	}
	defer client.Close()
	manifest, err := client.Handshake(ctx)
	if err != nil {
		return pluginhost.Plugin{}, err
	}
	if manifest.Source == "" {
		manifest.Source = path
	}
	return manifest, nil
}

func writePluginManifest(w io.Writer, manifest pluginhost.Plugin) {
	fmt.Fprintf(w, "Plugin: %s\n", manifest.Name)
	fmt.Fprintf(w, "Version: %s\n", manifest.Version)
	fmt.Fprintf(w, "Protocol: %s\n", manifest.Protocol)
	if manifest.Source != "" {
		fmt.Fprintf(w, "Source: %s\n", manifest.Source)
	}
	fmt.Fprintln(w, "Units:")
	for _, unit := range manifest.Units {
		fmt.Fprintf(w, "  - %s\n", unit.Kind)
		if unit.Title != "" {
			fmt.Fprintf(w, "    title: %s\n", unit.Title)
		}
		writeManifestPorts(w, "inputs", unit.Inputs)
		writeManifestPorts(w, "outputs", unit.Outputs)
		if len(unit.Artifacts) > 0 {
			fmt.Fprintln(w, "    artifacts:")
			for _, artifact := range unit.Artifacts {
				fmt.Fprintf(w, "      - %s", artifact.Name)
				if artifact.ContentType != "" {
					fmt.Fprintf(w, " (%s)", artifact.ContentType)
				}
				fmt.Fprintln(w)
			}
		}
	}
}

func writeManifestPorts(w io.Writer, title string, ports []contract.PortDef) {
	if len(ports) == 0 {
		return
	}
	fmt.Fprintf(w, "    %s:\n", title)
	for _, port := range ports {
		fmt.Fprintf(w, "      - %s %s\n", port.Name, port.Type)
	}
}
