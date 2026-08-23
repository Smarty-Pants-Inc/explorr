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
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/current-checkout\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(\"current checkout\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "jq"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "herdr"), []byte("#!/bin/sh\nprintf '%s\\n' 'old HerdR'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outerHerdR := filepath.Join(t.TempDir(), "outer-herdr")
	if err := os.WriteFile(outerHerdR, []byte(`#!/bin/sh
case "$1 $2" in
"plugin install")
	unset HERDR_BIN_PATH
	HERDR_BUILD_BIN_PATH="$0" exec /bin/sh "$EXPLORR_TEST_HERDR_INSTALL"
	;;
"pane split")
	test "${3:-}" = "--help" || exit 2
	printf '%s\n' '  --workspace string'
	;;
*)
	exit 2
	;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPLORR_TEST_HERDR_INSTALL", filepath.Join(pluginRoot, "install.sh"))
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

func TestHerdRManagedInstallRequiresWorkspaceSplit(t *testing.T) {
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
	for _, tool := range []string{"go", "jq"} {
		if err := os.WriteFile(filepath.Join(tools, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tools, "herdr"), []byte("#!/bin/sh\nprintf '%s\\n' '  --direction string'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	cmd := exec.Command("/bin/sh", "install.sh")
	cmd.Dir = pluginRoot
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "pane split --workspace is required") {
		t.Fatalf("old HerdR returned %v\n%s", err, output)
	}
}
