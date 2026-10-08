// =============================================================================
// File: install_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHerdRManagedInstallUsesManagerBinaryAndBuildsHostBinaryDespiteGOENV(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "herdr")
	if err := os.Mkdir(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("herdr/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "install.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "herdr-plugin.toml"), herdrPluginManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/current-checkout\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(\"current checkout\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "jq"), []byte("#!/bin/sh\ngrep -q '\"pattern\":\"^file://\"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "herdr"), []byte("#!/bin/sh\nprintf '%s\\n' 'old HerdR'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outerHerdR := filepath.Join(t.TempDir(), "outer-herdr")
	if err := os.WriteFile(outerHerdR, []byte(`#!/bin/sh
set -eu
if [ "$1 $2" = "plugin install" ]; then
	unset HERDR_BIN_PATH
	HERDR_BUILD_BIN_PATH="$0" exec /bin/sh "$EXPLORR_TEST_HERDR_INSTALL"
fi
case "$HERDR_SOCKET_PATH" in
  *explorr-herdr-capabilities-*/offline.sock) ;;
  *) echo "probe reached inherited socket" >&2; exit 2 ;;
esac
test -z "${HERDR_SESSION:-}${HERDR_PANE_ID:-}${HERDR_TAB_ID:-}${HERDR_WORKSPACE_ID:-}${HERDR_CLIENT_SOCKET_PATH:-}"
root="${HERDR_SOCKET_PATH%/offline.sock}"
test "$HOME" = "$root/home"
test "$XDG_CONFIG_HOME" = "$root/config"
test "$XDG_DATA_HOME" = "$root/data"
test "$HERDR_CONFIG_PATH" = "$root/config/herdr/config.toml"
case "$1 $2" in
"plugin pane")
	test "$3 $4" = "open --help"
	printf '%s\n' '  --placement <PLACEMENT>' '  --target-pane <PANE>' '  --direction <DIRECTION>'
	;;
"plugin link")
	test "$4" = "--disabled"
	grep -q 'min_herdr_version = "0.9.1"' "$3/herdr-plugin.toml"
	grep -q 'placement = "split"' "$3/herdr-plugin.toml"
	;;
"plugin list")
	printf '{"result":{"plugins":[{"plugin_id":"com.smartypants.explorr","panes":[{"id":"explorer","placement":"split"}],"link_handlers":[{"id":"local-file","pattern":"^file://","action":"open-file"}]}]}}\n'
	;;
*)
	exit 2
	;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPLORR_TEST_HERDR_INSTALL", filepath.Join(pluginRoot, "install.sh"))
	for _, key := range []string{"HERDR_SESSION", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "HERDR_CLIENT_SOCKET_PATH", "HERDR_CONFIG_PATH", "HERDR_SOCKET_PATH"} {
		t.Setenv(key, "must-not-reach-probe")
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	crossOS := "linux"
	if runtime.GOOS == crossOS {
		crossOS = "darwin"
	}
	crossArch := "amd64"
	if runtime.GOARCH == crossArch {
		crossArch = "arm64"
	}
	tuningName, tuningValue := "", ""
	switch runtime.GOARCH {
	case "amd64":
		tuningName, tuningValue = "GOAMD64", "v4"
	case "arm64":
		tuningName, tuningValue = "GOARM64", "v9.5"
	default:
		t.Skipf("no portable architecture tuning regression fixture for %s", runtime.GOARCH)
	}
	goenv := filepath.Join(t.TempDir(), "goenv")
	goenvContents := strings.Join([]string{
		"GOOS=" + crossOS,
		"GOARCH=" + crossArch,
		tuningName + "=" + tuningValue,
	}, "\n") + "\n"
	if err := os.WriteFile(goenv, []byte(goenvContents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", goenv)
	t.Setenv("GOOS", "")
	t.Setenv("GOARCH", "")
	t.Setenv(tuningName, "")
	if output, err := exec.Command("go", "env", "GOOS").Output(); err != nil || strings.TrimSpace(string(output)) != crossOS {
		t.Fatalf("GOENV target = %q, %v; want %q", output, err, crossOS)
	}
	if output, err := exec.Command("go", "env", tuningName).Output(); err != nil || strings.TrimSpace(string(output)) != tuningValue {
		t.Fatalf("GOENV %s = %q, %v; want %q", tuningName, output, err, tuningValue)
	}

	cmd := exec.Command(outerHerdR, "plugin", "install", "Smarty-Pants-Inc/explorr/herdr", "--yes")
	cmd.Dir = pluginRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("managed install failed: %v\n%s", err, output)
	}
	builtBinary := filepath.Join(pluginRoot, "bin", "explorr")
	buildInfo, err := exec.Command("go", "version", "-m", builtBinary).CombinedOutput()
	if err != nil {
		t.Fatalf("read built binary metadata: %v\n%s", err, buildInfo)
	}
	if strings.Contains(string(buildInfo), tuningName+"="+tuningValue) {
		t.Fatalf("built binary retained GOENV %s=%s:\n%s", tuningName, tuningValue, buildInfo)
	}
	output, err := exec.Command(builtBinary).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(output); got != "current checkout" {
		t.Fatalf("built binary output = %q, want current checkout", got)
	}
}

func TestHerdRManagedInstallRequiresGo(t *testing.T) {
	pluginRoot := filepath.Join(t.TempDir(), "herdr")
	if err := os.Mkdir(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("herdr/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "install.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/sh", "install.sh")
	cmd.Dir = pluginRoot
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Go is required but not found on PATH") {
		t.Fatalf("missing Go returned %v\n%s", err, output)
	}
}

func TestHerdRManagedInstallRequiresJQ(t *testing.T) {
	pluginRoot := filepath.Join(t.TempDir(), "herdr")
	if err := os.Mkdir(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("herdr/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "install.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/sh", "install.sh")
	cmd.Dir = pluginRoot
	cmd.Env = append(os.Environ(), "PATH="+binDir)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "jq is required but not found on PATH") {
		t.Fatalf("missing jq returned %v\n%s", err, output)
	}
}

func TestHerdRManagedInstallRequiresTargetedSplitAndLinks(t *testing.T) {
	for _, capability := range []string{"missing-target", "help-failed", "rejected", "missing-handler"} {
		t.Run(capability, func(t *testing.T) {
			pluginRoot := filepath.Join(t.TempDir(), "herdr")
			if err := os.Mkdir(pluginRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			script, err := os.ReadFile("herdr/install.sh")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pluginRoot, "install.sh"), script, 0o755); err != nil {
				t.Fatal(err)
			}
			tools := t.TempDir()
			if err := os.WriteFile(filepath.Join(tools, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tools, "jq"), []byte("#!/bin/sh\ngrep -q '\"pattern\":\"^file://\"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			herdr := `#!/bin/sh
set -eu
case "$1 $2" in
  "plugin pane")
    test "$3 $4" = "open --help"
    test "$EXPLORR_TEST_CAPABILITY" != "help-failed" || exit 2
    printf '%s\n' '  --placement <PLACEMENT>' '  --direction <DIRECTION>'
    if [ "$EXPLORR_TEST_CAPABILITY" != "missing-target" ]; then
      printf '%s\n' '  --target-pane <PANE>'
    fi
    ;;
  "plugin link")
    test "$4" = "--disabled"
    test "$EXPLORR_TEST_CAPABILITY" != "rejected"
    ;;
  "plugin list")
    printf '{"result":{"plugins":[]}}\n'
    ;;
  *) exit 2 ;;
esac
`
			if err := os.WriteFile(filepath.Join(tools, "herdr"), []byte(herdr), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("EXPLORR_TEST_CAPABILITY", capability)
			t.Setenv("HERDR_BUILD_BIN_PATH", "")
			cmd := exec.Command("/bin/sh", "install.sh")
			cmd.Dir = pluginRoot
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "targeted split plugin panes and local file-link handlers is required") {
				t.Fatalf("%s HerdR returned %v\n%s", capability, err, output)
			}
			if _, err := os.Stat(filepath.Join(pluginRoot, "bin")); !os.IsNotExist(err) {
				t.Fatalf("%s capability failure started build: %v", capability, err)
			}
		})
	}
}
