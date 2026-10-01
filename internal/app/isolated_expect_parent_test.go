//go:build linux || darwin

// =============================================================================
// File: internal/app/isolated_expect_parent_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/theme"
)

// isolatedExpectParentID formats a directory identity per the shared contract.
func isolatedExpectParentID(t *testing.T, dir string) string {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{
		uintString(uint64(st.Dev)), uintString(uint64(st.Ino)),
	}, ":")
}

// uintString avoids fmt so the formatting under test is spelled out.
func uintString(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// TestIsolatedExpectParentMatchBinds opens and binds when the held parent is
// exactly the identity the helper verified.
func TestIsolatedExpectParentMatchBinds(t *testing.T) {
	isolatedReceiverEnvironment(t)
	path, _ := isolatedSaveFiles(t)
	id := isolatedExpectParentID(t, filepath.Dir(path))
	a, err := NewIsolatedSingleFileAtExpecting(path, 1, 2, id, isolatedExpectFileID(t, path), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	tab := a.activeTabPtr()
	if !a.isolated || len(a.tabs) != 1 || tab.Path != path || tab.Cursor != posAt(0, 1) {
		t.Fatalf("expected-parent pane not opened at the location: %+v", tab)
	}
	got, err := tab.BoundParentID()
	if err != nil || got.String() != id {
		t.Fatalf("bound parent = %v, %v; want %s", got, err, id)
	}
	if a.initialTab != nil {
		t.Fatal("expectation leaked past construction into later navigation")
	}
	tab.InsertString("X") // cursor is at line 1, column 2
	isolatedPublisherMenu(t, a, "Save")
	isolatedSaveAssertBytes(t, path, "AXAA\n")
}

// TestIsolatedExpectParentMismatchFailsClosed: another directory's identity,
// or a parent replaced between the helper's check and the constructor, yields
// no app, an error, a finalized screen, and no writes anywhere.
func TestIsolatedExpectParentMismatchFailsClosed(t *testing.T) {
	for _, kind := range []string{"other-dir", "parent-replaced", "malformed", "image"} {
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
			parent := filepath.Dir(path)
			id := isolatedExpectParentID(t, parent)
			// The file identity is the genuine one; only the parent is wrong.
			fileID := isolatedExpectFileID(t, path)
			switch kind {
			case "other-dir":
				id = isolatedExpectParentID(t, filepath.Dir(victim))
			case "parent-replaced":
				// The helper verified `id`; the parent is then swapped for a
				// look-alike directory holding a different file of the same name.
				if err := os.Rename(parent, parent+"-held"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(parent, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("replacement\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				id = "1:2:3"
			case "image":
				path = filepath.Join(parent, "pic.png")
				if err := os.WriteFile(path, isolatedExpectPNG(), 0o644); err != nil {
					t.Fatal(err)
				}
				fileID = isolatedExpectFileID(t, path)
				// Image previews are checked too: another directory's identity refuses.
				id = isolatedExpectParentID(t, filepath.Dir(victim))
			}
			before := isolatedPublisherSnapshot(t)
			a, err := NewIsolatedSingleFileAtExpecting(path, 1, 1, id, fileID, t.TempDir())
			if a != nil || err == nil {
				if a != nil {
					a.Close()
				}
				t.Fatalf("mismatched parent appeared ready: app=%v error=%v", a, err)
			}
			if !strings.Contains(err.Error(), "parent") {
				t.Fatalf("failure is not the parent check: %v", err)
			}
			if screen != nil {
				// v3: the receiver binds, checks and acks BEFORE any screen exists.
				t.Fatal("refused receiver created a screen")
			}
			isolatedPublisherAssertUnchanged(t, before)
			isolatedSaveAssertBytes(t, victim, "BBB\n")
			switch kind {
			case "parent-replaced":
				isolatedSaveAssertBytes(t, path, "replacement\n")
				isolatedSaveAssertBytes(t, filepath.Join(parent+"-held", "file.txt"), "AAA\n")
			case "image":
			default:
				isolatedSaveAssertBytes(t, path, "AAA\n")
			}
		})
	}
}

// isolatedExpectPNG is a 1x1 PNG. Image previews are decoded from the
// identity-checked descriptors, so a mismatched parent must still fail closed.
func isolatedExpectPNG() []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// TestIsolatedWithoutExpectParentUnchanged: the link handler's constructor keeps
// working without an expectation.
func TestIsolatedWithoutExpectParentUnchanged(t *testing.T) {
	isolatedReceiverEnvironment(t)
	path, _ := isolatedSaveFiles(t)
	a, err := NewIsolatedSingleFileAt(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if a.initialTab != nil || len(a.tabs) != 1 {
		t.Fatal("plain isolated constructor changed")
	}
}
