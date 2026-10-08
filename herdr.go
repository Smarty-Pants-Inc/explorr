package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Smarty-Pants-Inc/explorr/internal/version"
)

const (
	herdrPluginID          = "com.smartypants.explorr"
	herdrCapabilityProbeID = "com.smartypants.explorr-capability-probe"
)

//go:embed herdr/herdr-plugin.toml
var herdrPluginManifest []byte

func herdrPluginDir() (string, error) {
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "explorr", "herdr"), nil
}

func herdrBinary() string {
	if path := strings.TrimSpace(os.Getenv("HERDR_BIN_PATH")); path != "" {
		return path
	}
	return "herdr"
}

func withoutEnvironmentKeys(env []string, keys ...string) []string {
	filtered := env[:0]
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		remove := false
		for _, candidate := range keys {
			if key == candidate {
				remove = true
				break
			}
		}
		if !remove {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func runHerdRWithEnvironment(env []string, args ...string) ([]byte, error) {
	cmd := exec.Command(herdrBinary(), args...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return nil, fmt.Errorf("herdr %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return output, nil
}

func runHerdR(args ...string) ([]byte, error) {
	return runHerdRWithEnvironment(os.Environ(), args...)
}

func checkExplorrOnPath() error {
	path, err := exec.LookPath("explorr")
	if err != nil {
		return errors.New("explorr is not on PATH; install the binary before linking its HerdR plugin")
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s --version: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	expected := "explorr " + version.Version
	if actual := strings.TrimSpace(string(output)); actual != expected {
		return fmt.Errorf("PATH resolves %s as %q; expected %q", path, actual, expected)
	}
	return nil
}
func checkJQOnPath() error {
	if _, err := exec.LookPath("jq"); err != nil {
		return errors.New("jq is not on PATH; install jq before linking Explorr's HerdR plugin")
	}
	return nil
}

// isolatedHerdREnvironment prevents capability probes from reaching a live session.
func isolatedHerdREnvironment(root string) []string {
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HERDR_") {
			env = append(env, entry)
		}
	}
	env = withoutEnvironmentKeys(env,
		"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
	)
	return append(env,
		"HOME="+filepath.Join(root, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"XDG_RUNTIME_DIR="+filepath.Join(root, "runtime"),
		"HERDR_CONFIG_PATH="+filepath.Join(root, "config", "herdr", "config.toml"),
		"HERDR_SOCKET_PATH="+filepath.Join(root, "offline.sock"),
	)
}

// checkHerdRCapabilities validates source-pane targeting and the actual plugin schema offline.
func checkHerdRCapabilities() error {
	const required = "Smarty HerdR 0.9.1+ with targeted split plugin panes and local file-link handlers is required"
	root, err := os.MkdirTemp("", "explorr-herdr-capabilities-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	env := isolatedHerdREnvironment(root)
	help, err := runHerdRWithEnvironment(env, "plugin", "pane", "open", "--help")
	if err != nil {
		return fmt.Errorf("%s: %w", required, err)
	}
	for _, flag := range []string{"--placement", "--target-pane", "--direction"} {
		found := false
		for _, field := range strings.Fields(string(help)) {
			if field == flag || strings.HasPrefix(field, flag+"=") {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s (missing plugin pane open %s)", required, flag)
		}
	}

	pluginDir := filepath.Join(root, "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return err
	}
	probe := `id = "` + herdrCapabilityProbeID + `"
name = "Explorr capability probe"
version = "0.0.0"
min_herdr_version = "0.9.1"
platforms = ["linux", "macos", "windows"]

[[actions]]
id = "open-file"
title = "Open file"
command = ["true"]

[[link_handlers]]
id = "local-file"
title = "Open local file"
pattern = "^file://"
action = "open-file"

[[panes]]
id = "explorer"
title = "explorer"
placement = "split"
command = ["true"]
`
	if err := os.WriteFile(filepath.Join(pluginDir, "herdr-plugin.toml"), []byte(probe), 0o644); err != nil {
		return err
	}
	if _, err := runHerdRWithEnvironment(env, "plugin", "link", pluginDir, "--disabled"); err != nil {
		return fmt.Errorf("%s: %w", required, err)
	}
	output, err := runHerdRWithEnvironment(env, "plugin", "list", "--plugin", herdrCapabilityProbeID, "--json")
	if err != nil {
		return err
	}
	var registry herdrPluginList
	if err := json.Unmarshal(output, &registry); err != nil {
		return fmt.Errorf("decode HerdR capability probe: %w", err)
	}
	for _, plugin := range registry.Result.Plugins {
		if plugin.PluginID != herdrCapabilityProbeID {
			continue
		}
		for _, handler := range plugin.LinkHandlers {
			if handler.ID != "local-file" || handler.Pattern != "^file://" || handler.Action != "open-file" {
				continue
			}
			for _, pane := range plugin.Panes {
				if pane.ID == "explorer" && pane.Placement == "split" {
					return nil
				}
			}
		}
	}
	return errors.New(required)
}

func preflightHerdRPlugin() error {
	if err := checkExplorrOnPath(); err != nil {
		return err
	}
	if err := checkJQOnPath(); err != nil {
		return err
	}
	return checkHerdRCapabilities()
}

func installHerdRPlugin() (string, error) {
	if err := preflightHerdRPlugin(); err != nil {
		return "", err
	}
	dir, err := herdrPluginDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	manifest := filepath.Join(dir, "herdr-plugin.toml")
	tmp, err := os.CreateTemp(dir, ".herdr-plugin.toml.*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(herdrPluginManifest); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, manifest); err != nil {
		return "", err
	}
	if _, err := runHerdR("plugin", "link", dir); err != nil {
		return "", err
	}
	if _, err := checkHerdRPlugin(); err != nil {
		return "", err
	}
	return manifest, nil
}

type herdrPluginList struct {
	Result struct {
		Plugins []struct {
			PluginID     string `json:"plugin_id"`
			ManifestPath string `json:"manifest_path"`
			Panes        []struct {
				ID        string `json:"id"`
				Placement string `json:"placement"`
			} `json:"panes"`
			LinkHandlers []struct {
				ID      string `json:"id"`
				Pattern string `json:"pattern"`
				Action  string `json:"action"`
			} `json:"link_handlers"`
		} `json:"plugins"`
	} `json:"result"`
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func checkHerdRPlugin() (string, error) {
	if err := preflightHerdRPlugin(); err != nil {
		return "", err
	}
	dir, err := herdrPluginDir()
	if err != nil {
		return "", err
	}
	manifest := filepath.Join(dir, "herdr-plugin.toml")
	installed, err := os.ReadFile(manifest)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(installed, herdrPluginManifest) {
		return "", fmt.Errorf("installed HerdR plugin manifest is stale: %s", manifest)
	}

	output, err := runHerdR("plugin", "list", "--plugin", herdrPluginID, "--json")
	if err != nil {
		return "", err
	}
	var registry herdrPluginList
	if err := json.Unmarshal(output, &registry); err != nil {
		return "", fmt.Errorf("decode HerdR plugin registry: %w", err)
	}
	expected, err := canonicalPath(manifest)
	if err != nil {
		return "", err
	}
	for _, plugin := range registry.Result.Plugins {
		if plugin.PluginID != herdrPluginID {
			continue
		}
		actual, err := canonicalPath(plugin.ManifestPath)
		if err == nil && actual == expected {
			return manifest, nil
		}
	}
	return "", fmt.Errorf("%s is not linked to %s", herdrPluginID, expected)
}

func removeHerdRPlugin() (string, error) {
	dir, err := herdrPluginDir()
	if err != nil {
		return "", err
	}
	if _, err := runHerdR("plugin", "unlink", herdrPluginID); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func runHerdRPluginCommand(action string) (string, error) {
	switch action {
	case "install":
		manifest, err := installHerdRPlugin()
		if err != nil {
			return "", err
		}
		return "Explorr HerdR plugin installed: " + manifest, nil
	case "check":
		manifest, err := checkHerdRPlugin()
		if err != nil {
			return "", err
		}
		return "Explorr HerdR plugin ready: " + manifest, nil
	case "remove":
		dir, err := removeHerdRPlugin()
		if err != nil {
			return "", err
		}
		return "Explorr HerdR plugin removed: " + dir, nil
	default:
		return "", fmt.Errorf("unknown HerdR plugin action %q", action)
	}
}
