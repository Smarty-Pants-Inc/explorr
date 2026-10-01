package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Smarty-Pants-Inc/explorr/internal/version"
)

func TestHerdRManifestKeepsExplicitExplorerAndBundledDispatch(t *testing.T) {
	manifest := string(herdrPluginManifest)
	for _, required := range []string{`min_herdr_version = "0.9.1"`, `pattern = "^file://"`, `action = "open-file"`, `placement = "split"`} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("manifest missing %s", required)
		}
	}
	for _, obsolete := range []string{"workspace_right", "[[startup]]", "[[events]]", "pane split --workspace"} {
		if strings.Contains(manifest, obsolete) {
			t.Fatalf("manifest still contains %s", obsolete)
		}
	}
	commands := strings.Split(manifest, "'''")
	if len(commands) != 5 {
		t.Fatalf("expected only the open-file and explicit explorer shell commands, got %d delimiters", len(commands)-1)
	}
	for _, action := range []struct {
		name, command, wantArgs string
	}{
		{"open-file", commands[1], "--herdr-open\nfile:///tmp/a%20b.go#L12\n"},
		{"explorer", commands[3], "--explorer\n"},
	} {
		t.Run(action.name, func(t *testing.T) {
			root, pathBin := t.TempDir(), t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			binary := "#!/bin/sh\nprintf '%s\\n' \"$HERDR_PANE_ID\" \"$@\"\n"
			bundled := filepath.Join(root, "bin", "explorr")
			if err := os.WriteFile(bundled, []byte(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pathBin, "explorr"), []byte("#!/bin/sh\nprintf 'fallback\\n'\n"+strings.TrimPrefix(binary, "#!/bin/sh\n")), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_PLUGIN_ROOT", root)
			t.Setenv("HERDR_PLUGIN_CLICKED_URL", "file:///tmp/a%20b.go#L12")
			t.Setenv("HERDR_PANE_ID", "origin:p2")
			t.Setenv("PATH", pathBin)
			output, err := exec.Command("/bin/sh", "-c", action.command).CombinedOutput()
			if want := "origin:p2\n" + action.wantArgs; err != nil || string(output) != want {
				t.Fatalf("bundled dispatch = %q, %v; want %q", output, err, want)
			}
			if err := os.Remove(bundled); err != nil {
				t.Fatal(err)
			}
			output, err = exec.Command("/bin/sh", "-c", action.command).CombinedOutput()
			if want := "fallback\norigin:p2\n" + action.wantArgs; err != nil || string(output) != want {
				t.Fatalf("global fallback dispatch = %q, %v; want %q", output, err, want)
			}
		})
	}
}

func TestIsolatedHerdREnvironmentRemovesSessionContext(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_CLIENT_SOCKET_PATH", "HERDR_CONFIG_PATH", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "HERDR_FUTURE_CONTEXT"} {
		t.Setenv(key, "live-fleet-value")
	}
	want := map[string]string{
		"HOME":              filepath.Join(root, "home"),
		"XDG_CONFIG_HOME":   filepath.Join(root, "config"),
		"XDG_DATA_HOME":     filepath.Join(root, "data"),
		"XDG_STATE_HOME":    filepath.Join(root, "state"),
		"XDG_CACHE_HOME":    filepath.Join(root, "cache"),
		"XDG_RUNTIME_DIR":   filepath.Join(root, "runtime"),
		"HERDR_CONFIG_PATH": filepath.Join(root, "config", "herdr", "config.toml"),
		"HERDR_SOCKET_PATH": filepath.Join(root, "offline.sock"),
	}
	for key := range want {
		t.Setenv(key, "live-fleet-value")
	}
	for _, entry := range isolatedHerdREnvironment(root) {
		key, value, _ := strings.Cut(entry, "=")
		if expected, ok := want[key]; ok {
			if value != expected {
				t.Fatalf("probe %s = %q; want %q", key, value, expected)
			}
			delete(want, key)
		} else if strings.HasPrefix(key, "HERDR_") {
			t.Fatalf("probe retained runtime context %s", entry)
		}
	}
	if len(want) != 0 {
		t.Fatalf("probe missing isolated paths: %v", want)
	}
}

func TestResolveArgsHerdRCommands(t *testing.T) {
	for _, action := range []string{"install", "check", "remove"} {
		got := resolveArgs([]string{"herdr", action})
		if got.Err != nil || got.Action != actionHerdR || got.HerdRAction != action {
			t.Fatalf("herdr %s resolved to %+v", action, got)
		}
	}
	for _, args := range [][]string{{"herdr"}, {"herdr", "update"}, {"herdr", "install", "extra"}} {
		if got := resolveArgs(args); got.Err == nil {
			t.Fatalf("accepted invalid command %q: %+v", strings.Join(args, " "), got)
		}
	}
}

func TestHerdRPluginInstallCheckRemove(t *testing.T) {
	dataHome := filepath.Join(t.TempDir(), "data")
	state := filepath.Join(t.TempDir(), "linked-root")
	binDir := t.TempDir()
	originalPath := os.Getenv("PATH")
	herdr := filepath.Join(binDir, "herdr")
	script := `#!/bin/sh
set -eu
case "$HERDR_SOCKET_PATH" in
  *explorr-herdr-capabilities-*/offline.sock)
    test -z "${HERDR_SESSION:-}${HERDR_PANE_ID:-}${HERDR_TAB_ID:-}${HERDR_WORKSPACE_ID:-}${HERDR_CLIENT_SOCKET_PATH:-}"
    root="${HERDR_SOCKET_PATH%/offline.sock}"
    test "$HOME" = "$root/home"
    test "$XDG_CONFIG_HOME" = "$root/config"
    test "$XDG_DATA_HOME" = "$root/data"
    test "$HERDR_CONFIG_PATH" = "$root/config/herdr/config.toml"
    ;;
  "$EXPLORR_TEST_HERDR_SOCKET") ;;
  *) echo "unexpected socket: $HERDR_SOCKET_PATH" >&2; exit 2 ;;
esac
state="${EXPLORR_TEST_HERDR_STATE:?missing state path}"
case "$1 $2" in
  "plugin pane")
    test "$3 $4" = "open --help"
    test "${EXPLORR_TEST_HERDR_HELP:-ok}" != "failed" || exit 2
    printf '%s\n' 'Usage: herdr plugin pane open [OPTIONS]' '  --placement <PLACEMENT>' '  --direction <DIRECTION>'
    if [ "${EXPLORR_TEST_HERDR_HELP:-ok}" != "missing-target" ]; then
      printf '%s\n' '  --target-pane <PANE>'
    fi
    ;;
  "plugin link")
    case "$3" in
      *explorr-herdr-capabilities-*)
        test "$4" = "--disabled"
        grep -q 'min_herdr_version = "0.9.1"' "$3/herdr-plugin.toml"
        grep -q 'placement = "split"' "$3/herdr-plugin.toml"
        test "${EXPLORR_TEST_HERDR_CAPABILITIES:-ok}" != "rejected" || exit 2
        ;;
      *) printf '%s' "$3" > "$state" ;;
    esac
    ;;
  "plugin list")
    if [ "${4:-}" = "com.smartypants.explorr-capability-probe" ]; then
      case "${EXPLORR_TEST_HERDR_CAPABILITIES:-ok}" in
        missing)
          printf '{"result":{"plugins":[{"plugin_id":"com.smartypants.explorr-capability-probe"}]}}\n'
          ;;
        malformed)
          printf 'not json\n'
          ;;
        *)
          placement=split
          pattern='^file://'
          case "${EXPLORR_TEST_HERDR_CAPABILITIES:-ok}" in
            wrong-pane) placement=overlay ;;
            wrong-handler) pattern='^https://' ;;
          esac
          printf '{"result":{"plugins":[{"plugin_id":"com.smartypants.explorr-capability-probe","panes":[{"id":"explorer","placement":"%s"}],"link_handlers":[{"id":"local-file","pattern":"%s","action":"open-file"}]}]}}\n' "$placement" "$pattern"
          ;;
      esac
    else
      root="$(cat "$state")"
      printf '{"result":{"plugins":[{"plugin_id":"com.smartypants.explorr","manifest_path":"%s/herdr-plugin.toml"}]}}\n' "$root"
    fi
    ;;
  "plugin unlink")
    test "$3" = "com.smartypants.explorr"
    rm -f "$state"
    ;;
  *)
    echo "unexpected arguments: $*" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(herdr, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExplorrVersion := func(value string) {
		t.Helper()
		script := "#!/bin/sh\nprintf 'explorr %s\\n' '" + value + "'\n"
		if err := os.WriteFile(filepath.Join(binDir, "explorr"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("HERDR_BIN_PATH", herdr)
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(t.TempDir(), "herdr.sock"))
	t.Setenv("EXPLORR_TEST_HERDR_SOCKET", os.Getenv("HERDR_SOCKET_PATH"))
	t.Setenv("EXPLORR_TEST_HERDR_STATE", state)
	for _, key := range []string{"HERDR_SESSION", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "HERDR_CLIENT_SOCKET_PATH"} {
		t.Setenv(key, "must-not-reach-probe")
	}
	manifest := filepath.Join(dataHome, "explorr", "herdr", "herdr-plugin.toml")

	t.Setenv("PATH", t.TempDir())
	if _, err := runHerdRPluginCommand("install"); err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("missing Explorr preflight returned %v", err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("missing Explorr published manifest: %v", err)
	}

	writeExplorrVersion("0.9.0")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)
	if _, err := runHerdRPluginCommand("install"); err == nil || !strings.Contains(err.Error(), "expected \"explorr "+version.Version+"\"") {
		t.Fatalf("wrong Explorr version preflight returned %v", err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("wrong Explorr version published manifest: %v", err)
	}

	writeExplorrVersion(version.Version)
	t.Setenv("PATH", binDir)
	if _, err := runHerdRPluginCommand("install"); err == nil || !strings.Contains(err.Error(), "jq is not on PATH") {
		t.Fatalf("missing jq preflight returned %v", err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("missing jq published manifest: %v", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("missing jq linked plugin: %v", err)
	}
	jq := filepath.Join(binDir, "jq")
	if err := os.WriteFile(jq, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)
	for _, help := range []string{"missing-target", "failed"} {
		t.Setenv("EXPLORR_TEST_HERDR_HELP", help)
		if _, err := runHerdRPluginCommand("install"); err == nil || !strings.Contains(err.Error(), "targeted split plugin panes") {
			t.Fatalf("%s pane help preflight returned %v", help, err)
		}
		if _, err := os.Stat(manifest); !os.IsNotExist(err) {
			t.Fatalf("%s pane help published manifest: %v", help, err)
		}
	}
	t.Setenv("EXPLORR_TEST_HERDR_HELP", "ok")

	for _, capability := range []string{"missing", "wrong-pane", "wrong-handler", "rejected", "malformed"} {
		t.Setenv("EXPLORR_TEST_HERDR_CAPABILITIES", capability)
		if _, err := runHerdRPluginCommand("install"); err == nil {
			t.Fatalf("%s capability preflight succeeded", capability)
		}
		if _, err := os.Stat(manifest); !os.IsNotExist(err) {
			t.Fatalf("%s capabilities published manifest: %v", capability, err)
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatalf("%s capabilities linked plugin: %v", capability, err)
		}
	}
	t.Setenv("EXPLORR_TEST_HERDR_CAPABILITIES", "ok")

	message, err := runHerdRPluginCommand("install")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, manifest) {
		t.Fatalf("install message %q does not name %s", message, manifest)
	}
	installed, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != string(herdrPluginManifest) {
		t.Fatal("installed manifest differs from the embedded source")
	}
	if _, err := runHerdRPluginCommand("install"); err != nil {
		t.Fatal(err)
	}

	if _, err := runHerdRPluginCommand("check"); err != nil {
		t.Fatal(err)
	}
	if _, err := runHerdRPluginCommand("check"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(jq); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if _, err := runHerdRPluginCommand("check"); err == nil || !strings.Contains(err.Error(), "jq is not on PATH") {
		t.Fatalf("check accepted missing jq: %v", err)
	}
	if err := os.WriteFile(jq, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)

	if err := os.WriteFile(manifest, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runHerdRPluginCommand("check"); err == nil {
		t.Fatal("check accepted a stale installed manifest")
	}
	if _, err := runHerdRPluginCommand("install"); err != nil {
		t.Fatal(err)
	}

	if _, err := runHerdRPluginCommand("remove"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(manifest)); !os.IsNotExist(err) {
		t.Fatalf("plugin directory remains after remove: %v", err)
	}
	if _, err := runHerdRPluginCommand("remove"); err != nil {
		t.Fatal(err)
	}
}
