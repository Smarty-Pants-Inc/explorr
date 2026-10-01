//go:build linux || darwin

// =============================================================================
// File: internal/app/isolated_debug_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/dap"
)

// isolatedDebugFixture writes a debuggable Go file plus a launch.json beside
// it, so both F5 routes (file-keyed and launch.json) have something to start.
func isolatedDebugFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch := filepath.Join(dir, ".vscode", "launch.json")
	if err := os.MkdirAll(filepath.Dir(launch), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"version":"0.2.0","configurations":[{"name":"Run","type":"go","request":"launch","program":"${workspaceFolder}"}]}`
	if err := os.WriteFile(launch, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// isolatedDebugSpec names an adapter that cannot exist, so an ordinary start
// proves the start path ran without building or running anything.
func isolatedDebugSpec(dir string) dap.LaunchSpec {
	return dap.LaunchSpec{
		Name:    "fake",
		Adapter: dap.Adapter{Name: "fake", AdapterID: "fake", Argv: [][]string{{filepath.Join(dir, "no-such-adapter")}}},
		Request: "launch",
		Target:  dir,
		Args:    map[string]interface{}{"program": dir},
	}
}

// TestIsolatedDebugRefused: a linked pane edits one file, and a debug build
// writes into its folder, so F5 (both routes) and the single start point refuse.
func TestIsolatedDebugRefused(t *testing.T) {
	isolatedReceiverEnvironment(t)
	path := isolatedDebugFixture(t)
	dir := filepath.Dir(path)
	a, err := NewIsolatedSingleFileAt(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	t.Cleanup(a.stopDebugSession)
	before, _ := os.ReadDir(dir)

	a.handleKey(tcell.NewEventKey(tcell.KeyF5, 0, tcell.ModNone))
	if a.debug != nil || a.paletteOpen {
		t.Fatalf("isolated F5 started a session or opened the launch picker (debug=%v palette=%v)", a.debug, a.paletteOpen)
	}
	isolatedMutatorRefused(t, a, "debug")

	a.statusMsg = ""
	a.menuStartDebug()
	if a.debug != nil {
		t.Fatal("isolated menu start began a session")
	}
	isolatedMutatorRefused(t, a, "debug")

	// The final execution point: a launch picker or palette entry that was
	// already pending still cannot start a session.
	a.statusMsg = ""
	a.startDebugSpec(isolatedDebugSpec(dir))
	if a.debug != nil || a.dapReg != nil {
		t.Fatal("isolated startDebugSpec began a session")
	}
	isolatedMutatorRefused(t, a, "debug")

	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("refused debug changed the folder: %d -> %d entries", len(before), len(after))
	}
}

// TestOrdinaryDebugStillStarts is the counterexample: ordinary editors keep F5.
func TestOrdinaryDebugStillStarts(t *testing.T) {
	isolatedReceiverEnvironment(t)
	path := isolatedDebugFixture(t)
	a, err := NewSingleFileAt(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	t.Cleanup(a.stopDebugSession)

	a.startDebugSpec(isolatedDebugSpec(filepath.Dir(path)))
	if a.debug == nil || !a.debug.starting {
		t.Fatalf("ordinary start did not begin a session (status %q)", a.statusMsg)
	}
	if strings.HasPrefix(a.statusMsg, isolatedRefusalPrefix) {
		t.Fatalf("ordinary debug refused: %q", a.statusMsg)
	}
	// The fake adapter cannot spawn; let the failure land so nothing leaks.
	pumpEvents(t, a, 2*time.Second, func() bool { return a.debug == nil })

	// F5's router reaches its ordinary checks (here: one already starting),
	// without spawning a real adapter from the fixture's launch.json.
	a.debug = &debugSession{adapter: "fake", starting: true}
	a.statusMsg = ""
	a.handleKey(tcell.NewEventKey(tcell.KeyF5, 0, tcell.ModNone))
	if !strings.Contains(a.statusMsg, "already running") {
		t.Fatalf("ordinary F5 did not reach its normal start checks: %q", a.statusMsg)
	}
	a.debug = nil
}
