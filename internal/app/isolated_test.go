//go:build linux || darwin

// =============================================================================
// File: internal/app/isolated_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/state"
	"github.com/Smarty-Pants-Inc/explorr/internal/theme"
)

// isolatedReceiverScreen delivers one input batch per frame, without sleeps,
// goroutines or host terminal access. Hooks write genuine requests after launch
// and inspect the front buffer at precisely the frame the user would see.
type isolatedReceiverScreen struct {
	tcell.SimulationScreen
	batches [][]tcell.Event
	pending []tcell.Event
	batch   int
	frames  int
	before  func(int)
	after   func(int)
}

// PollEvent starts the next batch only after Run has rendered the previous one.
func (s *isolatedReceiverScreen) PollEvent() tcell.Event {
	if len(s.pending) == 0 {
		if s.batch == len(s.batches) {
			return nil
		}
		if s.before != nil {
			s.before(s.batch)
		}
		s.pending = s.batches[s.batch]
		s.batch++
	}
	ev := s.pending[0]
	s.pending = s.pending[1:]
	return ev
}

// HasPendingEvent keeps separate batches from collapsing into a single frame.
func (s *isolatedReceiverScreen) HasPendingEvent() bool { return len(s.pending) != 0 }

// Show checks the actual rendered front buffer, not just the selected tab.
func (s *isolatedReceiverScreen) Show() {
	s.SimulationScreen.Show()
	if s.after != nil {
		s.after(s.frames)
	}
	s.frames++
}

// isolatedReceiverEnvironment isolates all state/config and substitutes only
// screen creation, so tests exercise the real public constructors and Run.
func isolatedReceiverEnvironment(t *testing.T) *isolatedReceiverScreen {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if got, want := state.Dir(), filepath.Join(xdg, "explorr"); got != want {
		t.Fatalf("state dir = %q, want %q", got, want)
	}
	s := &isolatedReceiverScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8")}
	previous := newScreen
	newScreen = func(theme.Theme) (tcell.Screen, error) {
		if err := s.Init(); err != nil {
			return nil, err
		}
		s.SetSize(120, 40)
		return s, nil
	}
	t.Cleanup(func() { newScreen = previous })
	return s
}

// isolatedReceiverFiles creates A and B in the SAME directory: withinRoot is
// deliberately satisfied, so isolation cannot accidentally pass by filtering B.
func isolatedReceiverFiles(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "exact-A.txt"), filepath.Join(dir, "sibling-B.txt")
	for path, text := range map[string]string{a: "AAA\n", b: "BBB\n"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return a, b
}

// isolatedReceiverWriteOpen uses the production atomic request writer.
func isolatedReceiverWriteOpen(t *testing.T, path string) {
	t.Helper()
	if err := state.WriteOpenRequest(path, 1, 1); err != nil {
		t.Fatal(err)
	}
}

// isolatedReceiverEditAndSave types into the receiver, then opens the actual
// hamburger menu and clicks Save. The coordinates come from production layout.
func isolatedReceiverEditAndSave(a *App) [][]tcell.Event {
	a.width, a.height = a.screen.Size()
	mx, my, _, _ := a.menuModalRect()
	items, _, _ := a.menuLayout()
	saveY := 0
	for _, item := range items {
		if item.label == "Save" {
			saveY = my + item.relY
		}
	}
	return [][]tcell.Event{
		{tcell.NewEventKey(tcell.KeyRune, 'X', tcell.ModNone)},
		{tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone)},
		{tcell.NewEventMouse(mx+3, saveY, tcell.Button1, tcell.ModNone)},
	}
}

// isolatedReceiverAssertFrame verifies identity and contents agree at Show.
func isolatedReceiverAssertFrame(t *testing.T, a *App, s *isolatedReceiverScreen, path string) {
	t.Helper()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != path {
		t.Fatalf("selected tab = %v, want %q", tab, path)
	}
	visible := screenAll(s.SimulationScreen)
	if !strings.Contains(visible, filepath.Base(path)) || !strings.Contains(visible, strings.TrimSpace(tab.Buffer.String())) {
		t.Fatalf("frame does not show selected %q and its buffer:\n%s", path, visible)
	}
}

// TestIsolatedReceiverOpenRequests proves both pre-constructor stale requests
// and fresh post-launch requests cannot retarget typing or menu Save to B.
func TestIsolatedReceiverOpenRequests(t *testing.T) {
	for _, timing := range []string{"before-constructor", "after-launch"} {
		t.Run(timing, func(t *testing.T) {
			s := isolatedReceiverEnvironment(t)
			pathA, pathB := isolatedReceiverFiles(t)
			if timing == "before-constructor" {
				isolatedReceiverWriteOpen(t, pathB)
			}
			a, err := NewIsolatedSingleFileAt(pathA, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			if !a.isolated || a.tree != nil || !a.withinRoot(pathB) {
				t.Fatal("constructor did not isolate a lean pane containing sibling B")
			}
			s.batches = append([][]tcell.Event{
				{tcell.NewEventResize(120, 40)},
				{tcell.NewEventFocus(true)},
			}, isolatedReceiverEditAndSave(a)...)
			s.before = func(batch int) {
				if timing == "after-launch" && batch == 0 {
					isolatedReceiverWriteOpen(t, pathB)
				}
			}
			s.after = func(int) { isolatedReceiverAssertFrame(t, a, s, pathA) }
			if err := a.Run(); err != nil {
				t.Fatal(err)
			}
			if len(a.tabs) != 1 || a.lastOpenSeq != 0 {
				t.Fatalf("global request reached isolated receiver: tabs=%d seq=%d", len(a.tabs), a.lastOpenSeq)
			}
			for path, want := range map[string]string{pathA: "XAAA\n", pathB: "BBB\n"} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("saved %q = %q (%v), want %q", path, got, err, want)
				}
			}
			if req, ok := state.ReadOpenRequest(); !ok || req.File != pathB {
				t.Fatal("isolated pane deleted a request intended for an ordinary editor")
			}
		})
	}
}

// TestIsolatedReceiverDebugRequests covers EVERY global debug action, including
// the file-selecting toggle and pathless commands. No adapter may be launched.
func TestIsolatedReceiverDebugRequests(t *testing.T) {
	for _, action := range state.DebugActions() {
		t.Run(action, func(t *testing.T) {
			s := isolatedReceiverEnvironment(t)
			pathA, pathB := isolatedReceiverFiles(t)
			a, err := NewIsolatedSingleFileAt(pathA, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			floor := a.lastDebugSeq
			s.batches = append([][]tcell.Event{{tcell.NewEventFocus(true)}}, isolatedReceiverEditAndSave(a)...)
			s.before = func(batch int) {
				if batch != 0 {
					return
				}
				file := ""
				if action == state.DebugActionToggleBreakpoint {
					file = pathB
				}
				if err := state.WriteDebugRequest(action, file, 1); err != nil {
					t.Fatal(err)
				}
				req, ok := state.ReadDebugRequest()
				if !ok || req.Seq <= floor || req.Seq <= debugRequestFloor {
					t.Fatal("debug probe is not fresh enough to exercise the receiver")
				}
				// Direct ingress must obey the same policy as the Run call site.
				a.consumeDebugRequest()
			}
			s.after = func(int) { isolatedReceiverAssertFrame(t, a, s, pathA) }
			if err := a.Run(); err != nil {
				t.Fatal(err)
			}
			if a.lastDebugSeq != floor || a.debug != nil || len(a.breakpoints) != 0 || len(a.tabs) != 1 {
				t.Fatal("global debug action mutated the isolated receiver")
			}
			for path, want := range map[string]string{pathA: "XAAA\n", pathB: "BBB\n"} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("debug probe saved %q = %q (%v), want %q", path, got, err, want)
				}
			}
			if req, ok := state.ReadDebugRequest(); !ok || req.Action != action {
				t.Fatal("isolated pane deleted another editor's debug request")
			}
		})
	}
}

// TestOrdinaryReceiverPanelJumps preserves project AND standalone integration.
// It also proves the first frame after consuming B actually shows B, not A.
func TestOrdinaryReceiverPanelJumps(t *testing.T) {
	for _, mode := range []string{"project", "standalone"} {
		for _, channel := range []string{"open", "debug-toggle"} {
			t.Run(mode+"/"+channel, func(t *testing.T) {
				s := isolatedReceiverEnvironment(t)
				pathA, pathB := isolatedReceiverFiles(t)
				var a *App
				var err error
				if mode == "project" {
					a, err = New(filepath.Dir(pathA))
					if err == nil {
						a.OpenFile(pathA)
					}
				} else {
					a, err = NewSingleFileAt(pathA, 1, 1)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(a.Close)
				if a.isolated {
					t.Fatal("ordinary editor unexpectedly isolated")
				}
				s.batches = [][]tcell.Event{{tcell.NewEventFocus(true)}}
				s.before = func(int) {
					if channel == "open" {
						isolatedReceiverWriteOpen(t, pathB)
					} else if err := state.WriteDebugRequest(state.DebugActionToggleBreakpoint, pathB, 1); err != nil {
						t.Fatal(err)
					}
				}
				s.after = func(frame int) {
					want := pathB
					if frame == 0 {
						want = pathA
					}
					isolatedReceiverAssertFrame(t, a, s, want)
				}
				if err := a.Run(); err != nil {
					t.Fatal(err)
				}
				if channel == "debug-toggle" {
					if mark, ok := a.activeTabPtr().MarkAt(0); !ok || !isBreakpointKind(mark.Kind) {
						t.Fatal("ordinary debug request no longer toggles a breakpoint")
					}
				}
			})
		}
	}
}

// TestIsolatedReceiverLocalActions remain user-intent actions, not IPC: local
// menu navigation may select B and keyboard debugging may toggle its breakpoint.
func TestIsolatedReceiverLocalActions(t *testing.T) {
	isolatedReceiverEnvironment(t)
	pathA, pathB := isolatedReceiverFiles(t)
	a, err := NewIsolatedSingleFileAt(pathA, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.width, a.height = a.screen.Size()
	a.activeTabPtr().InsertString(pathB + ":1\n")
	a.activeTabPtr().MoveCursorTo(posAt(0, 0), false)
	items, _, _ := a.menuLayout()
	found := false
	for _, item := range items {
		if item.label == "Go to location under cursor" {
			if !item.enabled(a) {
				t.Fatal("local navigation disabled by isolation")
			}
			item.action(a)
			found = true
		}
	}
	if !found || a.activeTabPtr().Path != pathB {
		t.Fatal("explicit local menu navigation could not select B")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyF9, 0, tcell.ModNone))
	if mark, ok := a.activeTabPtr().MarkAt(0); !ok || !isBreakpointKind(mark.Kind) {
		t.Fatal("local keyboard debugging disabled by isolation")
	}
	a.active.Flush()
	if a.bpStore != nil {
		a.bpStore.Flush()
	}
}

// TestIsolatedReceiverUnprotectedCounterexample demonstrates the attribution
// failure with isolation deliberately disabled, using the same constructor,
// genuine post-launch request, typing and menu Save as the security regression.
func TestIsolatedReceiverUnprotectedCounterexample(t *testing.T) {
	for _, channel := range []string{"open", "debug-toggle"} {
		t.Run(channel, func(t *testing.T) {
			s := isolatedReceiverEnvironment(t)
			pathA, pathB := isolatedReceiverFiles(t)
			a, err := NewIsolatedSingleFileAt(pathA, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			a.isolated = false // negative control: model the old receiver policy
			s.batches = append([][]tcell.Event{{tcell.NewEventFocus(true)}}, isolatedReceiverEditAndSave(a)...)
			s.before = func(batch int) {
				if batch != 0 {
					return
				}
				if channel == "open" {
					isolatedReceiverWriteOpen(t, pathB)
				} else if err := state.WriteDebugRequest(state.DebugActionToggleBreakpoint, pathB, 1); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Run(); err != nil {
				t.Fatal(err)
			}
			if tab := a.activeTabPtr(); tab == nil || tab.Path != pathB {
				t.Fatal("negative control did not select sibling B")
			}
			for path, want := range map[string]string{pathA: "AAA\n", pathB: "XBBB\n"} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("negative control %q = %q (%v), want %q", path, got, err, want)
				}
			}
		})
	}
}

// TestNewIsolatedSingleFileAtPosition preserves the 1-based cursor contract.
func TestNewIsolatedSingleFileAtPosition(t *testing.T) {
	isolatedReceiverEnvironment(t)
	pathA, _ := isolatedReceiverFiles(t)
	if err := os.WriteFile(pathA, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewIsolatedSingleFileAt(pathA, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if got, want := a.activeTabPtr().Cursor, posAt(1, 2); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
}

// TestNewIsolatedSingleFileAtScreenFailure preserves constructor failures.
func TestNewIsolatedSingleFileAtScreenFailure(t *testing.T) {
	isolatedReceiverEnvironment(t)
	want := errors.New("screen unavailable")
	newScreen = func(theme.Theme) (tcell.Screen, error) { return nil, want }
	if a, err := NewIsolatedSingleFileAt("unused.txt", 1, 1); a != nil || !errors.Is(err, want) {
		t.Fatalf("constructor error = (%v, %v), want (nil, %v)", a, err, want)
	}
}
