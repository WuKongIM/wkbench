package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	pluginConfigVersion = "wkbench.plugins/v1"
	pluginConfigRelPath = ".wkbench/plugins.yaml"
)

type pluginConfigFile struct {
	Version string              `yaml:"version,omitempty"`
	Plugins []pluginConfigEntry `yaml:"plugins"`
}

type pluginConfigEntry struct {
	Name    string `yaml:"name"`
	Path    string `yaml:"path"`
	Enabled *bool  `yaml:"enabled,omitempty"`
}

func (p pluginConfigEntry) isEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

func loadConfiguredPluginPaths(cliPaths []string, stderr io.Writer) ([]string, int) {
	configPath, cfg, ok, err := readProjectPluginConfig()
	if err != nil {
		fmt.Fprintf(stderr, "plugin config failed: %v\n", err)
		return nil, exitConfig
	}
	var paths []string
	if ok {
		projectDir := pluginConfigProjectDir(configPath)
		for _, plugin := range cfg.Plugins {
			if !plugin.isEnabled() {
				continue
			}
			if plugin.Name == "" {
				fmt.Fprintln(stderr, "plugin config failed: plugin name is required")
				return nil, exitConfig
			}
			if plugin.Path == "" {
				fmt.Fprintf(stderr, "plugin config failed: plugin %q path is required\n", plugin.Name)
				return nil, exitConfig
			}
			paths = append(paths, resolvePluginPath(projectDir, plugin.Path))
		}
	}
	paths = append(paths, cliPaths...)
	return dedupePluginPaths(paths), exitOK
}

func readProjectPluginConfig() (string, pluginConfigFile, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", pluginConfigFile{}, false, err
	}
	configPath, ok, err := findPluginConfig(cwd)
	if err != nil || !ok {
		return configPath, pluginConfigFile{}, ok, err
	}
	cfg, err := readPluginConfigFile(configPath)
	return configPath, cfg, true, err
}

func readPluginConfigFile(path string) (pluginConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pluginConfigFile{}, err
	}
	var cfg pluginConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return pluginConfigFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Version != "" && cfg.Version != pluginConfigVersion {
		return pluginConfigFile{}, fmt.Errorf("%s has unsupported version %q", path, cfg.Version)
	}
	return cfg, nil
}

func writePluginConfigFile(path string, cfg pluginConfigFile) error {
	if cfg.Version == "" {
		cfg.Version = pluginConfigVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func findPluginConfig(start string) (string, bool, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false, err
	}
	for {
		path := filepath.Join(dir, pluginConfigRelPath)
		if _, err := os.Stat(path); err == nil {
			return path, true, nil
		} else if !os.IsNotExist(err) {
			return "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

func pluginConfigPathForWrite() (string, pluginConfigFile, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", pluginConfigFile{}, err
	}
	if path, ok, err := findPluginConfig(cwd); err != nil || ok {
		if err != nil {
			return "", pluginConfigFile{}, err
		}
		cfg, err := readPluginConfigFile(path)
		return path, cfg, err
	}
	return filepath.Join(cwd, pluginConfigRelPath), pluginConfigFile{Version: pluginConfigVersion}, nil
}

func pluginConfigProjectDir(configPath string) string {
	return filepath.Dir(filepath.Dir(configPath))
}

func resolvePluginPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(projectDir, path))
}

func normalizePluginPathForConfig(configPath, path string) (string, error) {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		return path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	abs := filepath.Clean(filepath.Join(cwd, path))
	projectDir := pluginConfigProjectDir(configPath)
	rel, err := filepath.Rel(projectDir, abs)
	if err != nil {
		return abs, nil
	}
	return filepath.Clean(rel), nil
}

func dedupePluginPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		key := canonicalPluginPath(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, path)
	}
	return out
}

func canonicalPluginPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}
