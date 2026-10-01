//go:build linux || darwin

// =============================================================================
// File: internal/editor/secure_save_posix_test.go
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// secureSaveFiles loads and binds A alongside an unrelated same-parent B.
func secureSaveFiles(t *testing.T) (*Tab, string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	parent := filepath.Join(dir, "parent")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(parent, "A.txt"), filepath.Join(parent, "B.txt")
	secureSaveWrite(t, a, "original A")
	secureSaveWrite(t, b, "untouched B")
	tab, err := NewTab(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.BindOriginal(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tab.Close(); err != nil {
			t.Error(err)
		}
	})
	return tab, a, b
}

// secureSaveWrite seeds a fixture without swallowing filesystem failures.
func secureSaveWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

// secureSaveWant reads the actual file to check for unintended truncation.
func secureSaveWant(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}

// secureSaveReject checks both data safety and the tab's rejection state.
func secureSaveReject(t *testing.T, tab *Tab, pathA, pathB string) {
	t.Helper()
	mtime := tab.Mtime
	if err := tab.Save(); err == nil {
		t.Fatal("bound Save accepted changed identity")
	}
	if !tab.Dirty || tab.Path != pathA || !tab.Mtime.Equal(mtime) {
		t.Fatal("rejected save changed Dirty, displayed path, or Mtime")
	}
	secureSaveWant(t, pathB, "untouched B")
}

// TestBoundOriginalSaveUnchanged proves normal edits (including shortening and
// emptying a file) save through the verified descriptor and preserve B/mode.
func TestBoundOriginalSaveUnchanged(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	if err := os.Chmod(a, 0600); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"longer edited A\nsecond line\n", "short", ""} {
		tab.Buffer = NewBuffer(text)
		tab.Dirty = true
		if err := tab.Save(); err != nil {
			t.Fatal(err)
		}
		secureSaveWant(t, a, text)
		secureSaveWant(t, b, "untouched B")
		info, err := os.Stat(a)
		if err != nil {
			t.Fatal(err)
		}
		if tab.Dirty || tab.Path != a || !tab.Mtime.Equal(info.ModTime()) || info.Mode().Perm() != 0600 {
			t.Fatal("successful bound save state/mode mismatch")
		}
	}
}

// TestBoundOriginalSaveFinalSymlink is the exact dirty A -> same-parent B attack.
func TestBoundOriginalSaveFinalSymlink(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	tab.InsertString("edit ")
	old := a + ".old"
	if err := os.Rename(a, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	secureSaveReject(t, tab, a, b)
	secureSaveWant(t, old, "original A")
	if err := tab.Reload(); err == nil {
		t.Fatal("dirty bound Reload followed symlink")
	}
	tab.Dirty = false
	if err := tab.Reload(); err == nil || tab.Buffer.String() != "edit original A" {
		t.Fatal("clean bound Reload followed symlink or changed buffer")
	}
	// A rejected reload must not poison the binding; restoring the entry works.
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, a); err != nil {
		t.Fatal(err)
	}
	tab.Dirty = true
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	secureSaveWant(t, a, "edit original A")
}

// TestBoundOriginalRejectsSymlinkToSameInode proves NOFOLLOW is mandatory even
// when a following identity check alone would find the original inode.
func TestBoundOriginalRejectsSymlinkToSameInode(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	tab.InsertString("edit ")
	old := a + ".old"
	if err := os.Rename(a, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, a); err != nil {
		t.Fatal(err)
	}
	secureSaveReject(t, tab, a, b)
	tab.Dirty = false
	if err := tab.Reload(); err == nil {
		t.Fatal("Reload followed a symlink to the original inode")
	}
	secureSaveWant(t, old, "original A")
}

// TestBoundOriginalSaveReplacedAncestor checks both direct-parent and higher
// ancestor replacement, with either symlink redirection or a new regular tree.
func TestBoundOriginalSaveReplacedAncestor(t *testing.T) {
	for _, higher := range []bool{false, true} {
		for _, symlink := range []bool{false, true} {
			name := "parent-directory"
			if higher {
				name = "ancestor-directory"
			}
			if symlink {
				name += "-symlink"
			}
			t.Run(name, func(t *testing.T) {
				tab, a, _ := secureSaveFiles(t)
				tab.InsertString("edit ")
				parent := filepath.Dir(a)
				ancestor := parent
				if higher {
					ancestor = filepath.Dir(parent)
				}
				moved := ancestor + ".old"
				if err := os.Rename(ancestor, moved); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(moved) })
				destination := ancestor
				if symlink {
					destination = t.TempDir()
					if err := os.Symlink(destination, ancestor); err != nil {
						t.Fatal(err)
					}
				}
				if higher {
					destination = filepath.Join(destination, filepath.Base(parent))
				}
				if err := os.MkdirAll(destination, 0755); err != nil {
					t.Fatal(err)
				}
				redirected := filepath.Join(destination, filepath.Base(a))
				secureSaveWrite(t, redirected, "untouched B")
				secureSaveReject(t, tab, a, redirected)
				tab.Dirty = false
				if err := tab.Reload(); err == nil || tab.Buffer.String() != "edit original A" {
					t.Fatal("clean Reload rebound through replaced ancestor")
				}
				oldA := filepath.Join(moved, filepath.Base(a))
				if higher {
					oldA = filepath.Join(moved, filepath.Base(parent), filepath.Base(a))
				}
				secureSaveWant(t, oldA, "original A")
			})
		}
	}
}

// TestBoundOriginalReloadAtomicReplacement permits explicit clean regular-file
// rebinding, but neither Save nor a dirty Reload can silently adopt the new inode.
func TestBoundOriginalReloadAtomicReplacement(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	oldHandle := tab.original.original
	replacement := a + ".replacement"
	secureSaveWrite(t, replacement, "external replacement\n")
	if err := os.Rename(replacement, a); err != nil {
		t.Fatal(err)
	}
	tab.InsertString("edit ")
	if err := tab.Save(); err == nil {
		t.Fatal("Save accepted replacement regular file")
	}
	if err := tab.Reload(); err == nil || !tab.Dirty || tab.Buffer.String() != "edit original A" {
		t.Fatal("dirty Reload rebound or discarded changes")
	}
	secureSaveWant(t, a, "external replacement\n")
	tab.Dirty = false
	if err := tab.Reload(); err != nil {
		t.Fatal(err)
	}
	if tab.Buffer.String() != "external replacement\n" || tab.Path != a {
		t.Fatal("clean Reload did not adopt replacement")
	}
	if _, err := oldHandle.Stat(); err == nil {
		t.Fatal("clean Reload leaked previous original descriptor")
	}
	tab.MoveCursorTo(Position{}, false)
	tab.InsertString("safe ")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	secureSaveWant(t, a, "safe external replacement\n")
	secureSaveWant(t, b, "untouched B")
}

// TestBoundOriginalReloadInPlace keeps ordinary same-inode external edits usable.
func TestBoundOriginalReloadInPlace(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	secureSaveWrite(t, a, "external in-place edit")
	if err := tab.Reload(); err != nil {
		t.Fatal(err)
	}
	if tab.Buffer.String() != "external in-place edit" || tab.Dirty {
		t.Fatal("same-inode Reload did not refresh clean buffer")
	}
	tab.InsertString("safe ")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	secureSaveWant(t, a, "safe external in-place edit")
	secureSaveWant(t, b, "untouched B")
}

// TestBoundOriginalSaveNamespaceRace stresses the validation/write boundary.
// Any successful save must still write A's inode, even if its name becomes B's
// symlink concurrently; rejecting saves must retain the user's edits.
func TestBoundOriginalSaveNamespaceRace(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	pinned := a + ".pinned"
	if err := os.Link(a, pinned); err != nil {
		t.Fatal(err)
	}
	tab.Buffer = NewBuffer("edited A through bound descriptor")
	done := make(chan error, 1)
	go func() {
		tmp := a + ".swap"
		for i := 0; i < 300; i++ {
			if err := os.Symlink(b, tmp); err != nil {
				done <- err
				return
			}
			if err := os.Rename(tmp, a); err != nil {
				done <- err
				return
			}
			if err := os.Link(pinned, tmp); err != nil {
				done <- err
				return
			}
			if err := os.Rename(tmp, a); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 300; i++ {
		tab.Dirty = true
		err := tab.Save()
		if err != nil && !tab.Dirty {
			t.Error("rejected racing save lost Dirty")
		}
		if err == nil && tab.Dirty {
			t.Error("successful racing save retained Dirty")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	secureSaveWant(t, b, "untouched B")
	// After the adversary stops, the original entry is restored and usable.
	tab.Dirty = true
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	secureSaveWant(t, a, tab.Buffer.String())
}

// TestBoundOriginalRejectsPathChangeAndClose ensures neither Save As nor resource
// shutdown can downgrade a protected tab to ordinary path-based writing.
func TestBoundOriginalRejectsPathChangeAndClose(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	tab.InsertString("edit ")
	tab.Path = b
	if err := tab.Save(); err == nil || !tab.Dirty {
		t.Fatal("bound Save accepted path change")
	}
	if err := tab.Reload(); err == nil {
		t.Fatal("bound Reload accepted path change")
	}
	secureSaveWant(t, b, "untouched B")
	tab.Path = a
	parent, original := tab.original.parent, tab.original.original
	if err := tab.BindOriginal(); err == nil {
		t.Fatal("repeat BindOriginal could replace binding")
	}
	if err := tab.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tab.Close(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*os.File{parent, original} {
		if _, err := f.Stat(); err == nil {
			t.Fatal("Close leaked bound descriptor")
		}
	}
	if err := tab.Save(); err == nil || !tab.Dirty {
		t.Fatal("Save after Close did not fail closed")
	}
	if err := tab.Reload(); err == nil {
		t.Fatal("Reload after Close did not fail closed")
	}
	secureSaveWant(t, a, "original A")
}

// TestBoundOriginalRejectsSpecialEntry prevents FIFO replacement from blocking
// or being opened for writes without regular-file and identity validation.
func TestBoundOriginalRejectsSpecialEntry(t *testing.T) {
	tab, a, b := secureSaveFiles(t)
	tab.InsertString("edit ")
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(a, 0600); err != nil {
		t.Fatal(err)
	}
	secureSaveReject(t, tab, a, b)
	if err := tab.Reload(); err == nil {
		t.Fatal("Reload accepted FIFO")
	}
}

// TestBindOriginalRejectsChangedContents catches in-place drift before binding,
// where device/inode alone would still match the originally loaded file.
func TestBindOriginalRejectsChangedContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.txt")
	secureSaveWrite(t, path, "before")
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	secureSaveWrite(t, path, "after")
	if err := tab.BindOriginal(); err == nil {
		t.Fatal("BindOriginal accepted stale loaded contents")
	}
	tab.InsertString("edit ")
	if err := tab.Save(); err == nil || !tab.Dirty {
		t.Fatal("failed binding did not fail closed")
	}
	secureSaveWant(t, path, "after")
}

// TestBindOriginalLoadRaceRejectsReplacement closes the load-to-bind gap,
// including a symlink whose target happens to contain identical bytes.
func TestBindOriginalLoadRaceRejectsReplacement(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "regular"
		if symlink {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a, b := filepath.Join(dir, "A.txt"), filepath.Join(dir, "B.txt")
			secureSaveWrite(t, a, "same bytes")
			secureSaveWrite(t, b, "same bytes")
			tab, err := NewTab(a)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(a, a+".old"); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(b, a); err != nil {
					t.Fatal(err)
				}
			} else {
				secureSaveWrite(t, a, "same bytes")
			}
			if err := tab.BindOriginal(); err == nil {
				t.Fatal("BindOriginal accepted changed loaded entry")
			}
			tab.InsertString("edit ")
			if err := tab.Save(); err == nil || !tab.Dirty {
				t.Fatal("failed binding allowed ordinary Save fallback")
			}
			secureSaveWant(t, b, "same bytes")
			if err := tab.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
