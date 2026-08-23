package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Smarty-Pants-Inc/explorr/internal/version"
)

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
  "$EXPLORR_TEST_HERDR_SOCKET"|*explorr-herdr-capabilities-*/offline.sock) ;;
  *) echo "unexpected socket: $HERDR_SOCKET_PATH" >&2; exit 2 ;;
esac
state="${EXPLORR_TEST_HERDR_STATE:?missing state path}"
case "$1 $2" in
  "pane split")
    test "${3:-}" = "--help"
    if [ "${EXPLORR_TEST_HERDR_WORKSPACE_FLAG:-new}" = "old" ]; then
      printf '%s\n' 'Usage: herdr pane split [flags]' '  --direction string'
    else
      printf '%s\n' 'Usage: herdr pane split [flags]' '  --workspace string'
    fi
    ;;
  "plugin link")
    case "$3" in
      *explorr-herdr-capabilities-*) ;;
      *) printf '%s' "$3" > "$state" ;;
    esac
    ;;
  "plugin list")
    if [ "${4:-}" = "com.smartypants.explorr-capability-probe" ]; then
      if [ "${EXPLORR_TEST_HERDR_CAPABILITIES:-ok}" = "missing" ]; then
        printf '{"result":{"plugins":[{"plugin_id":"com.smartypants.explorr-capability-probe"}]}}\n'
      else
        printf '{"result":{"plugins":[{"plugin_id":"com.smartypants.explorr-capability-probe","panes":[{"placement":"workspace_right"}],"link_handlers":[{"id":"local-file"}]}]}}\n'
      fi
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
	t.Setenv("EXPLORR_TEST_HERDR_WORKSPACE_FLAG", "old")
	if _, err := runHerdRPluginCommand("install"); err == nil || !strings.Contains(err.Error(), "pane split --workspace is required") {
		t.Fatalf("missing pane split workspace flag preflight returned %v", err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("missing pane split workspace flag published manifest: %v", err)
	}
	t.Setenv("EXPLORR_TEST_HERDR_WORKSPACE_FLAG", "new")

	t.Setenv("EXPLORR_TEST_HERDR_CAPABILITIES", "missing")
	if _, err := runHerdRPluginCommand("install"); err == nil || !strings.Contains(err.Error(), "workspace-right") {
		t.Fatalf("missing capability preflight returned %v", err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("missing capabilities published manifest: %v", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("missing capabilities linked plugin: %v", err)
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
