// =============================================================================
// File: install_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHerdRManagedInstallBuildsCurrentCheckout(t *testing.T) {
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
