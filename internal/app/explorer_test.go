// =============================================================================
// File: internal/app/explorer_test.go
// Created: 2026-10-01
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// fakeExplorerHerdR records exact calls without touching the user's session.
func fakeExplorerHerdR(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX fake herdr executable")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HERDR_TEST_LOG\"\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	t.Setenv("HERDR_TEST_LOG", log)
	return log
}

// TestOpenFileInHerdRSplitIgnoresAmbientScope covers another workspace, another
// tab in the same workspace, and the valid same-workspace/same-tab case. The
// explicit pane handle, not any focus hint, determines the split's tab.
func TestOpenFileInHerdRSplitIgnoresAmbientScope(t *testing.T) {
	for _, tc := range []struct {
		name, workspace, tab, focusedPane string
	}{
		{"other workspace", "wB", "wB:t1", "wB:p1"},
		{"other tab", "wA", "wA:t2", "wA:p9"},
		{"same workspace and tab", "wA", "wA:t1", "wA:p7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeExplorerHerdR(t, `case "$1:$2" in
  pane:split) printf '%s\n' '{"result":{"pane":{"pane_id":"wA:p8","workspace_id":"wA","tab_id":"wA:t1"}}}' ;;
  pane:run) h=${4##*"--handoff '"}; h=${h%"'"}; : > "$h/ack"; printf '%s\n' '{"result":{}}' ;;
  *) printf '%s\n' '{"result":{}}' ;;
esac
`)
			t.Setenv("HERDR_PANE_ID", "wA:p7")
			t.Setenv("HERDR_WORKSPACE_ID", tc.workspace)
			t.Setenv("HERDR_TAB_ID", tc.tab)
			t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_id":"`+tc.focusedPane+`"}`)
			file := filepath.Join(t.TempDir(), "file.go")
			if err := os.WriteFile(file, []byte("package f\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := OpenFileInHerdRSplit(file, 1, 1); err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			want := "pane split --pane wA:p7 --direction right --cwd " + filepath.Dir(file) + " --no-focus"
			if len(lines) != 2 || lines[0] != want || !strings.HasPrefix(lines[1], "pane run wA:p8 exec ") {
				t.Fatalf("calls = %q; want explicit origin split/run without focus or snapshot fallback", lines)
			}
		})
	}
}

// TestOpenFileInHerdRSplitMissingOriginFailsClosed rejects even a usable-looking
// workspace/tab/context; a captured pane is required before invoking the CLI.
func TestOpenFileInHerdRSplitMissingOriginFailsClosed(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", " \t ")
	t.Setenv("HERDR_WORKSPACE_ID", "wA")
	t.Setenv("HERDR_TAB_ID", "wA:t1")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"wA","tab_id":"wA:t1","focused_pane_id":"wA:p7"}`)
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "must-not-run"))
	if err := OpenFileInHerdRSplit("file.go", 1, 1); err == nil || !strings.Contains(err.Error(), "originating pane") {
		t.Fatalf("missing origin error = %v", err)
	}
}

// TestOpenFileInHerdRSplitStaleOriginDoesNotRetry ensures a deleted/moved origin
// error is surfaced, not retried against a workspace or a focused pane.
func TestOpenFileInHerdRSplitStaleOriginDoesNotRetry(t *testing.T) {
	log := fakeExplorerHerdR(t, "printf '%s\\n' 'pane_not_found: originating pane closed' >&2\nexit 1\n")
	t.Setenv("HERDR_PANE_ID", "wA:p7")
	t.Setenv("HERDR_WORKSPACE_ID", "wB")
	file := filepath.Join(t.TempDir(), "file.go")
	if err := os.WriteFile(file, []byte("package f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := OpenFileInHerdRSplit(file, 1, 1); err == nil || !strings.Contains(err.Error(), "originating pane closed") {
		t.Fatalf("stale origin error = %v", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(calls)), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], "pane split --pane wA:p7 ") {
		t.Fatalf("calls = %q; failed origin must not trigger a fallback", lines)
	}
}

// TestExplorerActivationUsesOwnPane proves keyboard file launching goes through
// the same captured-origin path as --herdr-open, instead of --workspace.
func TestExplorerActivationUsesOwnPane(t *testing.T) {
	log := fakeExplorerHerdR(t, `case "$1:$2" in
  pane:split) printf '%s\n' '{"result":{"pane":{"pane_id":"wA:p8"}}}' ;;
  pane:run) h=${4##*"--handoff '"}; h=${h%"'"}; : > "$h/ack"; printf '%s\n' '{"result":{}}' ;;
  *) printf '%s\n' '{"result":{}}' ;;
esac
`)
	t.Setenv("HERDR_PANE_ID", "wA:p7")
	t.Setenv("HERDR_WORKSPACE_ID", "wB")
	t.Setenv("HERDR_TAB_ID", "wB:t2")
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	a.explorer = true
	a.tree.Focus(root, a.treeListHeight())
	a.handleTreeKey(keyEv(tcell.KeyDown, 0))
	a.handleTreeKey(keyEv(tcell.KeyEnter, 0))
	if done := waitExplorerDone(t, a); done.err != nil {
		t.Fatalf("explorer handoff: %v", done.err)
	}
	if a.confirmOpen || !a.tree.Focused || a.tree.ActiveFile != file || len(a.tabs) != 0 {
		t.Fatalf("activation failed: modal=%v focused=%v active=%q tabs=%d", a.confirmOpen, a.tree.Focused, a.tree.ActiveFile, len(a.tabs))
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(calls), "pane split --pane wA:p7 --direction right --cwd "+root+" --no-focus\n") || !strings.Contains(string(calls), " --single-file-at "+shellQuote(file)+" 1 1 --expect-parent ") {
		t.Fatalf("explorer launch = %q", calls)
	}
}

// TestExplorerMissingOriginShowsError proves the sidebar does not silently open
// a local editor or launch in whichever workspace another client last focused.
func TestExplorerMissingOriginShowsError(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_WORKSPACE_ID", "wA")
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "must-not-run"))
	a := newTestApp(t, t.TempDir())
	a.explorer = true
	a.openTreeFile(filepath.Join(a.rootDir, "file.go"))
	if !a.confirmOpen || a.confirmTitle != "Could not open file" || !strings.Contains(strings.Join(a.confirmMessageLines, "\n"), "originating pane") || len(a.tabs) != 0 {
		t.Fatalf("missing-origin modal = %v %q %q tabs=%d", a.confirmOpen, a.confirmTitle, a.confirmMessageLines, len(a.tabs))
	}
}

// waitExplorerDone pumps the explorer's screen until the async handoff result
// arrives, then handles it like the event loop does.
func waitExplorerDone(t *testing.T, a *App) *explorerOpenDoneEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if done, ok := ev.(*explorerOpenDoneEvent); ok {
			a.handleEvent(done)
			return done
		}
	}
	t.Fatal("explorer never reported the handoff result")
	return nil
}
