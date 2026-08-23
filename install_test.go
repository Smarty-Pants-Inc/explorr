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

func TestHerdRManagedInstallBuildsHostBinaryDespiteCrossTargetEnvironment(t *testing.T) {
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
	newHerdR := filepath.Join(t.TempDir(), "new-herdr")
	if err := os.WriteFile(newHerdR, []byte(`#!/bin/sh
test "$1 $2 $3" = "pane split --help" || exit 2
printf '%s\n' '  --workspace string'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", newHerdR)
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	crossOS := "linux"
	if runtime.GOOS == crossOS {
		crossOS = "darwin"
	}
	t.Setenv("GOOS", crossOS)
	t.Setenv("GOARCH", "amd64")

	cmd := exec.Command("/bin/sh", "install.sh")
	cmd.Dir = pluginRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("managed install failed: %v\n%s", err, output)
	}
	output, err := exec.Command(filepath.Join(pluginRoot, "bin", "explorr")).Output()
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
	oldHerdR := filepath.Join(t.TempDir(), "old-herdr")
	if err := os.WriteFile(oldHerdR, []byte("#!/bin/sh\nprintf '%s\\n' '  --direction string'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HERDR_BIN_PATH", oldHerdR)

	cmd := exec.Command("/bin/sh", "install.sh")
	cmd.Dir = pluginRoot
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "pane split --workspace is required") {
		t.Fatalf("old HerdR returned %v\n%s", err, output)
	}
}
