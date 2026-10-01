//go:build linux || darwin

// =============================================================================
// File: internal/app/isolated_publishers_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/dap"
	"github.com/Smarty-Pants-Inc/explorr/internal/state"
)

// isolatedPublisherFile records both bytes and identity: rewriting identical
// contents still violates channel ownership and must not pass this oracle.
type isolatedPublisherFile struct {
	data []byte
	info fs.FileInfo
}

// isolatedPublisherSnapshot captures all shared editor channels, including the
// input requests that the receiver guards must leave available to other editors.
func isolatedPublisherSnapshot(t *testing.T) map[string]isolatedPublisherFile {
	t.Helper()
	out := make(map[string]isolatedPublisherFile)
	for _, name := range []string{"active.json", "debug-session.json", "breakpoints.json", "open-request.json", "debug-request.json"} {
		path := filepath.Join(state.Dir(), name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			out[path] = isolatedPublisherFile{}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = isolatedPublisherFile{data: data, info: info}
	}
	return out
}

// isolatedPublisherAssertUnchanged also checks absence, mtime, mode and inode,
// catching shutdown's unconditional Flush even when no input was processed.
func isolatedPublisherAssertUnchanged(t *testing.T, before map[string]isolatedPublisherFile) {
	t.Helper()
	for path, want := range before {
		got, err := os.Stat(path)
		if want.info == nil {
			if !os.IsNotExist(err) {
				t.Fatalf("isolated pane created shared %s: stat = %v", path, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, want.data) || !os.SameFile(got, want.info) || !got.ModTime().Equal(want.info.ModTime()) || got.Mode() != want.info.Mode() {
			t.Fatalf("isolated pane modified shared %s: before %s, after %s", path, want.data, data)
		}
	}
}

// isolatedPublisherSeed uses stale but valid ordinary-editor snapshots and
// breakpoints for both this root and another root; no channel may be rewritten.
func isolatedPublisherSeed(t *testing.T, path, sibling string) {
	t.Helper()
	root := filepath.Dir(path)
	payloads := map[string]interface{}{
		"active.json":        state.Active{File: sibling, Line: 7, Col: 9, Root: root, TS: 1},
		"debug-session.json": state.DebugSession{State: state.DebugStateStopped, File: sibling, Line: 7, Root: root, TS: 1},
		"breakpoints.json": map[string][]state.PersistedBreakpoint{
			root:             {{Path: path, Line: 0, Enabled: true}, {Path: sibling, Line: 0, Enabled: false, Condition: "ordinary"}},
			"/other-project": {{Path: "/other-project/file.txt", Line: 4, Enabled: true}},
		},
		"open-request.json":  state.OpenRequest{File: sibling, Line: 1, Col: 1, Seq: 1},
		"debug-request.json": state.DebugRequest{Action: state.DebugActionStart, Seq: 1},
	}
	if err := os.MkdirAll(state.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range payloads {
		blob, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state.Dir(), name), blob, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// isolatedPublisherMenu invokes the registered, enabled main-menu action rather
// than bypassing menu reachability with a direct call to the implementation.
func isolatedPublisherMenu(t *testing.T, a *App, label string) {
	t.Helper()
	items, _, _ := a.menuLayout()
	for _, item := range items {
		if item.label == label {
			if item.enabled != nil && !item.enabled(a) {
				t.Fatalf("local menu action %q disabled", label)
			}
			item.action(a)
			return
		}
	}
	t.Fatalf("local menu action %q not registered", label)
}

// TestIsolatedPublishersLeaveSharedStateUntouched covers debounce publication,
// local breakpoint sync/clear, no-event Flush, EOF and explicit menu quit.
func TestIsolatedPublishersLeaveSharedStateUntouched(t *testing.T) {
	for _, seeded := range []bool{false, true} {
		for _, exit := range []string{"no-events", "eof", "menu-quit"} {
			name := "absent/" + exit
			if seeded {
				name = "stale/" + exit
			}
			t.Run(name, func(t *testing.T) {
				s := isolatedReceiverEnvironment(t)
				path, sibling := isolatedReceiverFiles(t)
				if seeded {
					isolatedPublisherSeed(t, path, sibling)
				}
				before := isolatedPublisherSnapshot(t)
				a, err := NewIsolatedSingleFileAt(path, 1, 2)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(a.Close)
				if !a.isolated || a.active != nil || a.debugPub != nil || a.bpStore != nil {
					t.Fatal("isolated constructor allocated a shared output channel")
				}
				if len(a.breakpoints) != 0 || len(a.activeTabPtr().MarkLines()) != 0 {
					t.Fatal("isolated constructor loaded shared breakpoint state")
				}
				isolatedPublisherAssertUnchanged(t, before)
				if exit != "no-events" {
					s.batches = [][]tcell.Event{
						{tcell.NewEventResize(120, 40), tcell.NewEventFocus(true)},
						{tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), tcell.NewEventKey(tcell.KeyF9, 0, tcell.ModNone)},
						{tcell.NewEventFocus(true)},
						{tcell.NewEventFocus(false)},
						{tcell.NewEventFocus(true)},
					}
					s.before = func(batch int) {
						switch batch {
						case 2:
							if got := a.allBreakpoints(); len(got) != 1 || got[0].Path != path {
								t.Fatalf("F9 did not sync a local breakpoint: %+v", got)
							}
							isolatedPublisherMenu(t, a, "Set breakpoint condition")
							if !a.promptOpen {
								t.Fatal("local condition prompt did not open")
							}
							a.conditionSubmit("local == 1")
							a.closeAllModals()
							isolatedPublisherMenu(t, a, "Toggle breakpoint enabled")
						case 3:
							bps := a.allBreakpoints()
							if len(bps) != 1 || bps[0].Condition != "local == 1" || bps[0].Enabled {
								t.Fatalf("local breakpoint menu changes not reconciled: %+v", bps)
							}
							isolatedPublisherMenu(t, a, "List breakpoints")
							if !a.paletteOpen || len(a.paletteOverride) != 1 {
								t.Fatal("local breakpoint list not usable")
							}
							a.closeAllModals()
							isolatedPublisherMenu(t, a, "Clear breakpoints")
						case 4:
							isolatedPublisherMenu(t, a, "Toggle breakpoint")
							if exit == "menu-quit" {
								isolatedPublisherMenu(t, a, "Quit editor")
							}
						}
					}
				}
				// Direct publication and Flush must also be harmless outside Run.
				a.publishActive()
				a.publishDebug()
				a.publishDebugIdle()
				a.active.Flush()
				a.debugPub.Flush()
				a.bpStore.Flush()
				s.after = func(int) { isolatedPublisherAssertUnchanged(t, before) }
				if err := a.Run(); err != nil {
					t.Fatal(err)
				}
				if exit != "no-events" && len(a.allBreakpoints()) != 1 {
					t.Fatal("local model was disabled along with persistence")
				}
				a.Close()
				isolatedPublisherAssertUnchanged(t, before)
				// Leave enough time for any accidentally scheduled debounce writer.
				time.Sleep(200 * time.Millisecond)
				isolatedPublisherAssertUnchanged(t, before)
			})
		}
	}
}

// TestOrdinaryPublishersRemainEnabled is the negative control: all ordinary
// constructors bootstrap shared breakpoints and publish real cursor/debug state.
func TestOrdinaryPublishersRemainEnabled(t *testing.T) {
	for _, mode := range []string{"project", "single-file", "single-file-at"} {
		t.Run(mode, func(t *testing.T) {
			s := isolatedReceiverEnvironment(t)
			path, sibling := isolatedReceiverFiles(t)
			isolatedPublisherSeed(t, path, sibling)
			// Receiver behavior has its own regression tests; keep stale input
			// from deliberately retargeting this publication probe.
			for _, path := range []string{state.OpenRequestFile(), state.DebugRequestFile()} {
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
			case "single-file-at":
				a, err = NewSingleFileAt(path, 1, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			if a.isolated || a.active == nil || a.debugPub == nil || a.bpStore == nil || len(a.breakpoints) != 2 {
				t.Fatal("ordinary constructor lost shared channels or breakpoint bootstrap")
			}
			a.debug = &debugSession{adapter: "fake", stopped: true, path: path, line: 0}
			s.batches = [][]tcell.Event{{tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), tcell.NewEventKey(tcell.KeyF9, 0, tcell.ModNone)}}
			s.after = func(frame int) {
				if frame == 0 {
					return
				}
				a.active.Flush()
				a.debugPub.Flush()
				var active state.Active
				isolatedPublisherReadJSON(t, filepath.Join(state.Dir(), "active.json"), &active)
				if active.File != path || active.Line != 1 || active.Col != 2 || active.Root != a.rootDir || active.TS <= 1 {
					t.Fatalf("ordinary active publication = %+v", active)
				}
				var debug state.DebugSession
				isolatedPublisherReadJSON(t, state.DebugSessionFile(), &debug)
				if debug.State != state.DebugStateStopped || debug.File != path || debug.Line != 1 || debug.BreakpointTotal != 2 {
					t.Fatalf("ordinary debug publication = %+v", debug)
				}
			}
			if err := a.Run(); err != nil {
				t.Fatal(err)
			}
			var debug state.DebugSession
			isolatedPublisherReadJSON(t, state.DebugSessionFile(), &debug)
			if debug.State != state.DebugStateIdle || debug.File != "" || debug.Root != a.rootDir || a.debug != nil {
				t.Fatalf("ordinary exit did not publish idle/stop local session: %+v", debug)
			}
			bps := state.LoadBreakpoints(a.rootDir)
			if len(bps) != 2 || !bps[0].Enabled || bps[0].Path != path || len(state.LoadBreakpoints("/other-project")) != 1 {
				t.Fatalf("ordinary breakpoint flush lost local/other-root state: %+v", bps)
			}
		})
	}
}

// isolatedPublisherReadJSON reads the actual wire file without a debounce wait;
// callers use explicit Flush or Run's shutdown to make publication deterministic.
func isolatedPublisherReadJSON(t *testing.T, path string, out interface{}) {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(blob, out); err != nil {
		t.Fatal(err)
	}
}

// TestIsolatedPublishersLocalDebugControlsAndTeardown proves publisher removal
// does not disable adapter traffic: F5/F10, live breakpoint resend and Run's
// synchronous disconnect all reach a real DAP connection to a recording fake.
func TestIsolatedPublishersLocalDebugControlsAndTeardown(t *testing.T) {
	s := isolatedReceiverEnvironment(t)
	path, sibling := isolatedReceiverFiles(t)
	isolatedPublisherSeed(t, path, sibling)
	before := isolatedPublisherSnapshot(t)
	a, err := NewIsolatedSingleFileAt(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	client, fake := newRecordingAdapter(t)
	t.Cleanup(client.Stop)
	a.debug = &debugSession{
		client: client, adapter: "fake", stopped: true, path: path, threadID: 1,
		caps: dap.Capabilities{SupportsConditionalBreakpoints: true},
	}
	seen := func(command string) bool {
		for _, req := range fake.requests() {
			if req.Command == command {
				return true
			}
		}
		return false
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyF5, 0, tcell.ModNone))
	if a.debug.stopped || !pumpEvents(t, a, 5*time.Second, func() bool { return seen("continue") }) {
		t.Fatal("isolated F5 did not continue the local adapter")
	}
	a.debug.stopped = true
	a.handleKey(tcell.NewEventKey(tcell.KeyF10, 0, tcell.ModNone))
	if a.debug.stopped || !pumpEvents(t, a, 5*time.Second, func() bool { return seen("next") }) {
		t.Fatal("isolated F10 did not step the local adapter")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyF9, 0, tcell.ModNone))
	if !pumpEvents(t, a, 5*time.Second, func() bool { return seen("setBreakpoints") }) {
		t.Fatal("isolated breakpoint edit did not reach the local adapter")
	}
	if bps := a.enabledBreakpoints(); len(bps) != 1 || bps[0].Path != path {
		t.Fatalf("local adapter breakpoint model = %+v", bps)
	}
	isolatedPublisherMenu(t, a, "Debug actions")
	if !a.paletteOpen || a.paletteTitle != "Debug" {
		t.Fatal("isolated debug picker disabled")
	}
	a.closeAllModals()
	s.batches = [][]tcell.Event{{tcell.NewEventFocus(true)}}
	if err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if a.debug != nil || !seen("disconnect") {
		t.Fatal("isolated Run exit did not synchronously stop the local adapter")
	}
	a.Close()
	isolatedPublisherAssertUnchanged(t, before)
}
