//go:build !linux && !darwin

// =============================================================================
// File: internal/app/isolated_unsupported_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/state"
	"github.com/Smarty-Pants-Inc/explorr/internal/theme"
)

// isolatedUnsupportedScreen records failed-constructor terminal cleanup.
type isolatedUnsupportedScreen struct {
	tcell.SimulationScreen
	finalized bool
}

// Fini records cleanup while preserving the simulation screen's behavior.
func (s *isolatedUnsupportedScreen) Fini() {
	s.finalized = true
	s.SimulationScreen.Fini()
}

// isolatedUnsupportedFile captures identity as well as bytes to detect rewrites.
type isolatedUnsupportedFile struct {
	info fs.FileInfo
	data []byte
}

// isolatedUnsupportedSnapshot includes all shared state, not just known channels,
// so unwanted undo persistence, temporary files and directory creation are caught.
func isolatedUnsupportedSnapshot(t *testing.T, roots ...string) map[string]isolatedUnsupportedFile {
	t.Helper()
	out := make(map[string]isolatedUnsupportedFile)
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			out[root] = isolatedUnsupportedFile{}
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			var data []byte
			if !entry.IsDir() {
				data, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			out[path] = isolatedUnsupportedFile{info: info, data: data}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// TestNewIsolatedSingleFileAtUnsupportedFailsClosed exercises the public app
// boundary: unsupported binding must not yield a ready pane or touch disk/state.
// Ordinary standalone editing must still work without requesting that binding.
func TestNewIsolatedSingleFileAtUnsupportedFailsClosed(t *testing.T) {
	for _, seeded := range []bool{false, true} {
		name := "absent-state"
		if seeded {
			name = "seeded-state"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			previous := newScreen
			var screens []*isolatedUnsupportedScreen
			newScreen = func(theme.Theme) (tcell.Screen, error) {
				s := &isolatedUnsupportedScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8")}
				if err := s.Init(); err != nil {
					return nil, err
				}
				s.SetSize(120, 40)
				screens = append(screens, s)
				return s, nil
			}
			t.Cleanup(func() { newScreen = previous })
			path := filepath.Join(t.TempDir(), "original.txt")
			if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if seeded {
				if err := os.MkdirAll(state.Dir(), 0o755); err != nil {
					t.Fatal(err)
				}
				for name, data := range map[string]string{
					"active.json":        `{"file":"other.txt","line":7,"col":9,"ts":1}`,
					"debug-session.json": `{"state":"stopped","file":"other.txt","line":7,"ts":1}`,
					"breakpoints.json":   `{}`,
					"open-request.json":  `{"file":"other.txt","line":1,"col":1,"seq":1}`,
					"debug-request.json": `{"action":"start","seq":1}`,
				} {
					if err := os.WriteFile(filepath.Join(state.Dir(), name), []byte(data), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := isolatedUnsupportedSnapshot(t, path, state.Dir())
			a, err := NewIsolatedSingleFileAt(path, 2, 2)
			if a != nil {
				a.Close()
				t.Fatalf("unsupported binding returned a ready app: error=%v", err)
			}
			if err == nil || !strings.HasPrefix(err.Error(), "cannot bind "+path+": ") ||
				!strings.Contains(err.Error(), "bind original: bound-original saving is unsupported on this platform") {
				t.Fatalf("constructor did not propagate unsupported binding: %v", err)
			}
			if len(screens) != 1 || !screens[0].finalized {
				t.Fatal("failed isolated constructor did not finalize its screen")
			}
			after := isolatedUnsupportedSnapshot(t, path, state.Dir())
			if len(after) != len(before) {
				t.Fatalf("failed constructor changed original/shared state entries: before=%d after=%d", len(before), len(after))
			}
			for path, want := range before {
				got, ok := after[path]
				if !ok || !bytes.Equal(got.data, want.data) || (got.info == nil) != (want.info == nil) {
					t.Fatalf("failed constructor changed original/shared state %s", path)
				}
				if want.info != nil && (!os.SameFile(got.info, want.info) || got.info.Mode() != want.info.Mode() || !got.info.ModTime().Equal(want.info.ModTime())) {
					t.Fatalf("failed constructor rewrote original/shared state %s", path)
				}
			}

			a, err = NewSingleFileAt(path, 2, 2)
			if err != nil || a == nil {
				t.Fatalf("ordinary constructor unavailable: app=%v error=%v", a, err)
			}
			t.Cleanup(a.Close)
			tab := a.activeTabPtr()
			if a.isolated || a.tree != nil || a.active == nil || a.debugPub == nil || a.bpStore == nil || tab == nil || tab.Path != path || tab.Cursor.Line != 1 || tab.Cursor.Col != 1 {
				t.Fatal("ordinary standalone constructor lost its tab, position or shared channels")
			}
			tab.InsertString("X")
			if err := tab.Save(); err != nil || tab.Dirty {
				t.Fatalf("ordinary editing/save disabled by unsupported binding: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "one\ntXwo\n" {
				t.Fatalf("ordinary save = %q (%v), want edited original", data, err)
			}
			a.Close()
			if len(screens) != 2 || !screens[1].finalized {
				t.Fatal("ordinary constructor did not retain normal screen cleanup")
			}
		})
	}
}
