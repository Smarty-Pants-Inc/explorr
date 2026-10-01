//go:build linux || darwin

// =============================================================================
// File: internal/app/isolated_save_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/format"
	"github.com/Smarty-Pants-Inc/explorr/internal/lsp"
	"github.com/Smarty-Pants-Inc/explorr/internal/state"
	"github.com/Smarty-Pants-Inc/explorr/internal/theme"
)

// isolatedSaveAssertBytes checks actual disk contents, including symlink targets.
func isolatedSaveAssertBytes(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q (%v), want %q", path, got, err, want)
	}
}

// isolatedSaveReplace changes the namespace AFTER the real constructor has loaded
// and bound the original. The returned restoration lets a rejected save recover
// without Reload, proving rejection did not drop the binding.
func isolatedSaveReplace(t *testing.T, path, victim, kind string) (held string, restore func()) {
	t.Helper()
	if strings.HasPrefix(kind, "parent-") {
		parent := filepath.Dir(path)
		heldParent := parent + "-held"
		if err := os.Rename(parent, heldParent); err != nil {
			t.Fatal(err)
		}
		if kind == "parent-symlink" {
			if err := os.Symlink(filepath.Dir(victim), parent); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Mkdir(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("replacement\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(heldParent, filepath.Base(path)), func() {
			if err := os.RemoveAll(parent); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(heldParent, parent); err != nil {
				t.Fatal(err)
			}
		}
	}
	held = path + ".held"
	if err := os.Rename(path, held); err != nil {
		t.Fatal(err)
	}
	if kind == "symlink" {
		if err := os.Symlink(victim, path); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, []byte("replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return held, func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(held, path); err != nil {
			t.Fatal(err)
		}
	}
}

// isolatedSaveFiles uses separate parents with the same basename so both final
// entry replacement and parent redirection have a genuine victim to protect.
func isolatedSaveFiles(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	paths := []string{filepath.Join(root, "original", "file.txt"), filepath.Join(root, "victim", "file.txt")}
	for i, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		text := "AAA\n"
		if i == 1 {
			text = "BBB\n"
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return paths[0], paths[1]
}

// TestIsolatedSaveNativeMenuRejection types and clicks the actual hamburger Save
// through Run. Every final visible frame must retain edits and show the error.
func TestIsolatedSaveNativeMenuRejection(t *testing.T) {
	for _, kind := range []string{"symlink", "regular", "parent-symlink", "parent-directory"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedReceiverEnvironment(t)
			path, victim := isolatedSaveFiles(t)
			a, err := NewIsolatedSingleFileAt(path, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			held, _ := isolatedSaveReplace(t, path, victim, kind)
			s.batches = isolatedReceiverEditAndSave(a)
			s.after = func(frame int) {
				if frame != len(s.batches) {
					return
				}
				if len(a.tabs) != 1 || !a.tabs[0].Dirty || a.tabs[0].Buffer.String() != "XAAA\n" {
					t.Fatal("rejected Save discarded or retargeted edited tab")
				}
				visible := screenAll(s.SimulationScreen)
				if !strings.Contains(a.statusMsg, "Save failed:") || !strings.Contains(visible, "Save failed:") || !strings.Contains(visible, "XAAA") {
					t.Fatalf("missing visible save error/edits: %q\n%s", a.statusMsg, visible)
				}
			}
			if err := a.Run(); err != nil {
				t.Fatal(err)
			}
			if s.frames != len(s.batches)+1 {
				t.Fatalf("did not observe every real frame: %d", s.frames)
			}
			isolatedSaveAssertBytes(t, held, "AAA\n")
			isolatedSaveAssertBytes(t, victim, "BBB\n")
			want := "replacement\n"
			if strings.Contains(kind, "symlink") {
				want = "BBB\n"
			}
			isolatedSaveAssertBytes(t, path, want)
		})
	}
}

// TestIsolatedSaveRejectedCloseRetainsBinding covers all save-before-close/quit
// branches and cancellation. Restoring the original lets the SAME dirty tab save.
func TestIsolatedSaveRejectedCloseRetainsBinding(t *testing.T) {
	for _, action := range []string{"Save", "Save & close tab", "Close tab", "Quit editor"} {
		t.Run(action, func(t *testing.T) {
			isolatedReceiverEnvironment(t)
			path, victim := isolatedSaveFiles(t)
			a, err := NewIsolatedSingleFileAt(path, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			tab := a.activeTabPtr()
			tab.InsertString("X")
			held, restore := isolatedSaveReplace(t, path, victim, "symlink")
			isolatedPublisherMenu(t, a, action)
			if action == "Close tab" || action == "Quit editor" {
				if !a.dirtyOpen {
					t.Fatal("dirty save confirmation missing")
				}
				a.dirtySave()
			}
			if a.quit || len(a.tabs) != 1 || a.activeTabPtr() != tab || !tab.Dirty || !strings.Contains(a.statusMsg, "Save failed:") {
				t.Fatalf("rejected %s lost dirty tab/binding: %q", action, a.statusMsg)
			}
			isolatedSaveAssertBytes(t, held, "AAA\n")
			isolatedSaveAssertBytes(t, victim, "BBB\n")
			isolatedPublisherMenu(t, a, "Close tab")
			a.dirtyCancel()
			restore()
			isolatedPublisherMenu(t, a, "Save")
			if tab.Dirty || !strings.HasPrefix(a.statusMsg, "Saved ") {
				t.Fatalf("rejection/cancel dropped live binding: %q", a.statusMsg)
			}
			isolatedSaveAssertBytes(t, path, "XAAA\n")
		})
	}
}

// isolatedSaveScreen records failed-constructor finalization without touching
// production screen creation or bypassing the real constructor.
type isolatedSaveScreen struct {
	tcell.Screen
	finalized bool
}

// Fini records cleanup and delegates to the simulation screen.
func (s *isolatedSaveScreen) Fini() {
	s.finalized = true
	s.Screen.Fini()
}

// TestIsolatedSaveConstructorFailsClosed rejects both load and binding errors,
// especially NewTab's ordinary missing-file fallback, without fake readiness.
func TestIsolatedSaveConstructorFailsClosed(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			isolatedReceiverEnvironment(t)
			factory := newScreen
			var screen *isolatedSaveScreen
			newScreen = func(th theme.Theme) (tcell.Screen, error) {
				s, err := factory(th)
				screen = &isolatedSaveScreen{Screen: s}
				return screen, err
			}
			path, victim := isolatedSaveFiles(t)
			switch kind {
			case "missing":
				path += ".missing"
			case "directory":
				path = filepath.Dir(path)
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(victim, path); err != nil {
					t.Fatal(err)
				}
			}
			before := isolatedPublisherSnapshot(t)
			if a, err := NewIsolatedSingleFileAt(path, 1, 1); a != nil || err == nil {
				if a != nil {
					a.Close()
				}
				t.Fatalf("failed original appeared ready: app=%v error=%v", a, err)
			}
			if screen == nil || !screen.finalized {
				t.Fatal("failed constructor did not release screen")
			}
			if kind == "missing" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("constructor created missing original: %v", err)
				}
			}
			isolatedSaveAssertBytes(t, victim, "BBB\n")
			isolatedPublisherAssertUnchanged(t, before)
		})
	}
}

// TestIsolatedSaveExplicitNavigationBinds every explicitly loaded local tab,
// while failed local opens keep the old tab instead of registering a fallback.
func TestIsolatedSaveExplicitNavigationBinds(t *testing.T) {
	isolatedReceiverEnvironment(t)
	path, sibling := isolatedReceiverFiles(t)
	if err := os.WriteFile(path, []byte(sibling+":2:2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewIsolatedSingleFileAt(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	isolatedPublisherMenu(t, a, "Go to location under cursor")
	tab := a.activeTabPtr()
	if len(a.tabs) != 2 || tab.Path != sibling || tab.Cursor != posAt(1, 1) {
		t.Fatalf("local navigation failed: %v", tab)
	}
	tab.MoveCursorTo(posAt(0, 0), false)
	tab.InsertString("X")
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("victim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, restore := isolatedSaveReplace(t, sibling, victim, "symlink")
	isolatedPublisherMenu(t, a, "Save")
	if !tab.Dirty || !strings.Contains(a.statusMsg, "Save failed:") {
		t.Fatal("explicit navigation registered an unbound tab")
	}
	isolatedSaveAssertBytes(t, victim, "victim\n")
	isolatedSaveAssertBytes(t, held, "one\ntwo\n")
	restore()
	isolatedPublisherMenu(t, a, "Save")
	isolatedSaveAssertBytes(t, sibling, "Xone\ntwo\n")
	if err := a.openFileAt(path+".missing", 99, 99); err == nil {
		t.Fatal("local missing file accepted")
	}
	if len(a.tabs) != 2 || a.activeTabPtr() != tab || tab.Cursor != posAt(0, 1) || !strings.Contains(a.statusMsg, "Error:") {
		t.Fatal("failed local open registered or repositioned a tab")
	}
	link := filepath.Join(filepath.Dir(path), "link.txt")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	a.OpenFile(link)
	if len(a.tabs) != 2 || a.activeTabPtr() != tab || !strings.Contains(a.statusMsg, "Error:") {
		t.Fatal("public local open fell back after failed bind")
	}
}

// isolatedSaveHeldFDs counts the native held file and parent descriptors on Linux.
// Darwin still exercises closed-bound Save/Reload through the public API below.
func isolatedSaveHeldFDs(t *testing.T, path string) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		return 0
	}
	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && (os.SameFile(info, file) || os.SameFile(info, parent)) {
			n++
		}
	}
	return n
}

// TestIsolatedSaveShutdownReleasesHandles covers actual closes and both Run exits;
// repeating cleanup never re-enables ordinary writes or leaks native held FDs.
func TestIsolatedSaveShutdownReleasesHandles(t *testing.T) {
	for _, exit := range []string{"clean-close", "save-close", "discard-close", "app-close", "run-eof", "run-quit", "run-discard-quit"} {
		t.Run(exit, func(t *testing.T) {
			s := isolatedReceiverEnvironment(t)
			path, _ := isolatedReceiverFiles(t)
			before := isolatedSaveHeldFDs(t, path)
			a, err := NewIsolatedSingleFileAt(path, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			tab := a.activeTabPtr()
			if runtime.GOOS == "linux" && isolatedSaveHeldFDs(t, path) != before+2 {
				t.Fatal("constructor did not retain native original and parent handles")
			}
			switch exit {
			case "clean-close":
				isolatedPublisherMenu(t, a, "Close tab")
			case "save-close":
				tab.InsertString("X")
				isolatedPublisherMenu(t, a, "Save & close tab")
				isolatedSaveAssertBytes(t, path, "XAAA\n")
			case "discard-close":
				tab.InsertString("X")
				isolatedPublisherMenu(t, a, "Close tab")
				a.dirtyDiscard()
			case "app-close":
				a.Close()
			default:
				if exit != "run-eof" {
					s.batches = [][]tcell.Event{{tcell.NewEventFocus(true)}}
					s.before = func(int) {
						if exit == "run-discard-quit" {
							tab.InsertString("X")
						}
						isolatedPublisherMenu(t, a, "Quit editor")
						if a.dirtyOpen {
							a.dirtyDiscard()
						}
					}
				}
				if err := a.Run(); err != nil {
					t.Fatal(err)
				}
			}
			if got := isolatedSaveHeldFDs(t, path); got != before {
				t.Fatalf("%s leaked held native handles: before=%d after=%d", exit, before, got)
			}
			if _, err := os.Stat(filepath.Join(state.Dir(), "undo")); !os.IsNotExist(err) {
				t.Fatalf("%s invoked pathname-based undo persistence: %v", exit, err)
			}
			if err := tab.Save(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("%s left Save enabled: %v", exit, err)
			}
			if err := tab.Reload(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("%s left Reload enabled: %v", exit, err)
			}
			a.Close()
			a.Close()
			if err := tab.Close(); err != nil {
				t.Fatalf("non-idempotent cleanup: %v", err)
			}
		})
	}
}

// TestIsolatedSaveReadOnlyImageLinks preserves isolated image-link previews:
// their Save predicate is disabled and even forced menu Save fails before writes.
func TestIsolatedSaveReadOnlyImageLinks(t *testing.T) {
	s := isolatedReceiverEnvironment(t)
	dir := t.TempDir()
	original, link := filepath.Join(dir, "original.png"), filepath.Join(dir, "link.png")
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
	a, err := NewIsolatedSingleFileAt(link, 1, 1)
	if err != nil {
		t.Fatalf("read-only image link failed initial open: %v", err)
	}
	t.Cleanup(a.Close)
	s.batches = [][]tcell.Event{{tcell.NewEventFocus(true)}}
	s.before = func(int) {
		if !a.activeTabPtr().IsImage() || a.hasSavableTab() {
			t.Fatal("image link did not remain a read-only tab")
		}
		// The UI disables this row; a forced invocation still has native protection.
		a.menuSave()
		if !strings.Contains(a.statusMsg, "Save failed: image tabs are read-only") {
			t.Fatalf("forced image Save did not fail safely: %q", a.statusMsg)
		}
	}
	if err := a.Run(); err != nil {
		t.Fatal(err)
	}
	isolatedSaveAssertBytes(t, original, data.String())
	a.Close()
	if err := a.activeTabPtr().Save(); err == nil {
		t.Fatal("image Save became writable after shutdown")
	}
}

// TestIsolatedSaveGeneratedViewsStayReadOnly covers the existing generated-view
// creation path, which bypasses NewTab and never owns a disk original to bind.
func TestIsolatedSaveGeneratedViewsStayReadOnly(t *testing.T) {
	isolatedReceiverEnvironment(t)
	path, _ := isolatedReceiverFiles(t)
	a, err := NewIsolatedSingleFileAt(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.diagnostics = map[string][]lsp.Diagnostic{path: {{Message: "test diagnostic"}}}
	isolatedPublisherMenu(t, a, "Problems")
	if !a.activeTabPtr().Synthetic || a.hasSavableTab() {
		t.Fatal("generated view was not read-only")
	}
	// Even accidentally assigning a disk path must not authorize a generated Save.
	a.activeTabPtr().Path = path
	a.menuSave()
	if !strings.Contains(a.statusMsg, "cannot be saved") {
		t.Fatalf("forced generated Save did not fail safely: %q", a.statusMsg)
	}
	a.Close()
	isolatedSaveAssertBytes(t, path, "AAA\n")
}

// isolatedSaveFormatScreen receives one bounded formatter completion instead of
// polling or sleeping; production execFormatter still runs the real command.
type isolatedSaveFormatScreen struct {
	tcell.Screen
	done chan *formatDoneEvent
}

// PostEvent intercepts only formatter completion, leaving other events alone.
func (s *isolatedSaveFormatScreen) PostEvent(ev tcell.Event) error {
	if done, ok := ev.(*formatDoneEvent); ok {
		s.done <- done
		return nil
	}
	return s.Screen.PostEvent(ev)
}

// TestIsolatedSaveSkipsPathFormatter proves the post-write attack: an ordinary
// formatter can rename the saved original and write through its replacement
// symlink. Isolated native Save never starts that path-based command.
func TestIsolatedSaveSkipsPathFormatter(t *testing.T) {
	for _, mode := range []string{"isolated", "ordinary", "bound-with-old-hook"} {
		t.Run(mode, func(t *testing.T) {
			isolatedReceiverEnvironment(t)
			useTestTrustFile(t)
			path, victim := isolatedSaveFiles(t)
			marker := filepath.Join(t.TempDir(), "formatter-ran")
			// Code owns this finite shell program; no generated command is executed.
			argv := []string{"/bin/sh", "-c", `set -e; mv "$1" "$1.held"; ln -s "$2" "$1"; printf 'formatted\n' > "$1"; printf 'ran\n' > "$3"`, "formatter", "$FILE", victim, marker}
			blob, err := json.Marshal(format.Config{Commands: map[string][]string{"txt": argv}})
			if err != nil {
				t.Fatal(err)
			}
			writeFormatConfig(t, filepath.Dir(path), string(blob))
			preTrust(t, filepath.Dir(path), true)
			var a *App
			if mode == "ordinary" {
				a, err = NewSingleFileAt(path, 1, 1)
			} else {
				a, err = NewIsolatedSingleFileAt(path, 1, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			if mode == "bound-with-old-hook" {
				a.isolated = false // negative control: bound Save plus the old formatter hook
			}
			screen := &isolatedSaveFormatScreen{Screen: a.screen, done: make(chan *formatDoneEvent, 1)}
			a.screen = screen
			a.activeTabPtr().InsertString("X")
			isolatedPublisherMenu(t, a, "Save")
			if mode != "isolated" || !strings.HasPrefix(a.statusMsg, "Saved ") {
				// Join even a regression's unexpected launch before leaving this test.
				select {
				case done := <-screen.done:
					if done.err != nil {
						t.Fatalf("formatter counterexample failed: %v", done.err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("formatter completion deadline exceeded")
				}
			}
			if mode == "isolated" {
				if !strings.HasPrefix(a.statusMsg, "Saved ") || a.confirmOpen {
					t.Fatalf("isolated Save resumed the path-based hook: %q", a.statusMsg)
				}
				isolatedSaveAssertBytes(t, path, "XAAA\n")
				isolatedSaveAssertBytes(t, victim, "BBB\n")
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("isolated formatter ran: %v", err)
				}
			} else {
				isolatedSaveAssertBytes(t, path+".held", "XAAA\n")
				isolatedSaveAssertBytes(t, victim, "formatted\n")
				isolatedSaveAssertBytes(t, marker, "ran\n")
			}
			if a.activeTabPtr().Dirty {
				t.Fatal("native Save did not complete before formatter decision")
			}
			if err := a.Run(); err != nil {
				t.Fatal(err)
			}
			a.Close()
			if mode == "isolated" {
				if _, err := os.Stat(filepath.Join(state.Dir(), "undo")); !os.IsNotExist(err) {
					t.Fatalf("isolated save/Run/Close resumed pathname-based undo persistence: %v", err)
				}
			}
		})
	}
}

// TestIsolatedSaveOrdinaryTrustPromptPreserved proves the same menu Save still
// reaches ordinary trust checks, while isolated saves skip the whole auto hook.
func TestIsolatedSaveOrdinaryTrustPromptPreserved(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "isolated"}[isolated], func(t *testing.T) {
			isolatedReceiverEnvironment(t)
			useTestTrustFile(t)
			path, _ := isolatedReceiverFiles(t)
			writeFormatConfig(t, filepath.Dir(path), `{"commands":{"txt":["echo","$FILE"]}}`)
			a, err := newSingleFileAt(path, 1, 1, isolated, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			a.activeTabPtr().InsertString("X")
			isolatedPublisherMenu(t, a, "Save")
			if a.confirmOpen == isolated || (!isolated && a.confirmCancelHook == nil) {
				t.Fatalf("ordinary trust flow changed or isolated hook resumed: confirm=%v", a.confirmOpen)
			}
			isolatedSaveAssertBytes(t, path, "XAAA\n")
			if !isolated {
				a.confirmCancel()
			}
		})
	}
}

// TestIsolatedSaveOrdinarySafeSave preserves the ordinary constructors' native
// Save paths, including the traditional missing-file-on-first-save behavior.
func TestIsolatedSaveOrdinarySafeSave(t *testing.T) {
	for _, mode := range []string{"project", "single-file", "single-file-at", "isolated", "ordinary-missing"} {
		t.Run(mode, func(t *testing.T) {
			s := isolatedReceiverEnvironment(t)
			path, _ := isolatedReceiverFiles(t)
			if mode == "ordinary-missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			var a *App
			var err error
			switch mode {
			case "project":
				a, err = New(filepath.Dir(path))
				if err == nil {
					a.OpenFile(path)
				}
			case "single-file":
				a, err = NewSingleFile(path)
			case "isolated":
				a, err = NewIsolatedSingleFileAt(path, 1, 1)
			default:
				a, err = NewSingleFileAt(path, 1, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			s.batches = isolatedReceiverEditAndSave(a)
			mx, my, _, _ := a.menuButtonRect()
			s.batches[1] = []tcell.Event{tcell.NewEventMouse(mx+1, my, tcell.Button1, tcell.ModNone)}
			if err := a.Run(); err != nil {
				t.Fatal(err)
			}
			want := "XAAA\n"
			if mode == "ordinary-missing" {
				want = "X"
			}
			isolatedSaveAssertBytes(t, path, want)
			if a.activeTabPtr().Dirty || !strings.HasPrefix(a.statusMsg, "Saved ") {
				t.Fatalf("safe menu Save failed: %q", a.statusMsg)
			}
			if mode != "isolated" {
				// Ordinary Close/Run cleanup must remain harmless to ordinary Save.
				a.activeTabPtr().InsertString("Y")
				if err := a.activeTabPtr().Save(); err != nil {
					t.Fatalf("ordinary cleanup changed Save semantics: %v", err)
				}
			}
		})
	}
}
