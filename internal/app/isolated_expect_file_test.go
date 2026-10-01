//go:build linux || darwin

// =============================================================================
// File: internal/app/isolated_expect_file_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/theme"
)

// isolatedExpectFileID formats a pathname's OWN identity (lstat, never
// following a final symlink) per the shared DEV:INO contract, exactly as
// Reviewr's no-follow openat+fstat reports a regular file.
func isolatedExpectFileID(t *testing.T, path string) string {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return uintString(uint64(st.Dev)) + ":" + uintString(uint64(st.Ino))
}

// isolatedExpectFilePair creates A.md and B.md in ONE directory and returns
// their paths with the identities Reviewr verified for A: its held parent and
// A's own file identity.
func isolatedExpectFilePair(t *testing.T) (a, b, parentID, fileID string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "docs")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a, b = filepath.Join(dir, "A.md"), filepath.Join(dir, "B.md")
	for path, text := range map[string]string{a: "AAA\n", b: "BBB\n"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return a, b, isolatedExpectParentID(t, dir), isolatedExpectFileID(t, a)
}

// TestIsolatedExpectFileSameParentSwapFailsClosed is the P2: after Reviewr
// verified A, A's NAME is redirected inside the SAME parent, so the parent
// identity still matches. Each swap must yield no app, an error, a released
// screen, no shared state, and no write to B (or the replacement).
func TestIsolatedExpectFileSameParentSwapFailsClosed(t *testing.T) {
	for _, kind := range []string{"symlink-to-B", "hardlink-to-B", "atomic-replacement", "wrong-file-id"} {
		t.Run(kind, func(t *testing.T) {
			isolatedReceiverEnvironment(t)
			factory := newScreen
			var screen *isolatedSaveScreen
			newScreen = func(th theme.Theme) (tcell.Screen, error) {
				s, err := factory(th)
				screen = &isolatedSaveScreen{Screen: s}
				return screen, err
			}
			a, b, parentID, fileID := isolatedExpectFilePair(t)
			wantA := "AAA\n"
			switch kind {
			case "symlink-to-B":
				// The exact reported swap: A.md -> B.md in the same folder.
				if err := os.Remove(a); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("B.md", a); err != nil {
					t.Fatal(err)
				}
				wantA = "BBB\n" // reading through the link
			case "hardlink-to-B":
				// A regular file now, but B's inode: no-follow alone cannot tell.
				if err := os.Remove(a); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(b, a); err != nil {
					t.Fatal(err)
				}
				wantA = "BBB\n"
			case "atomic-replacement":
				tmp := filepath.Join(filepath.Dir(a), ".A.md.tmp")
				if err := os.WriteFile(tmp, []byte("CCC\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(tmp, a); err != nil {
					t.Fatal(err)
				}
				wantA = "CCC\n"
			case "wrong-file-id":
				// Unchanged namespace, but the expectation names another file.
				fileID = isolatedExpectFileID(t, b)
			}
			// A symlink may reuse A's freed inode number (seen on ext4), so the
			// FILE ID alone could match it; the O_NOFOLLOW bind refuses it anyway.
			if kind != "wrong-file-id" && kind != "symlink-to-B" && isolatedExpectFileID(t, a) == fileID {
				t.Fatal("swap reused A's identity; the test proves nothing")
			}
			if isolatedExpectParentID(t, filepath.Dir(a)) != parentID {
				t.Fatal("parent identity changed; this is not the same-parent swap")
			}
			before := isolatedPublisherSnapshot(t)
			app, err := NewIsolatedSingleFileAtExpecting(a, 1, 1, parentID, fileID)
			if app != nil || err == nil {
				if app != nil {
					// Prove the harm the expectation must prevent, then fail.
					app.activeTabPtr().InsertString("X")
					isolatedPublisherMenu(t, app, "Save")
					app.Close()
				}
				t.Fatalf("same-parent swap %s appeared ready: app=%v error=%v", kind, app != nil, err)
			}
			if screen == nil || !screen.finalized {
				t.Fatal("failed constructor did not release its screen")
			}
			isolatedPublisherAssertUnchanged(t, before)
			isolatedSaveAssertBytes(t, b, "BBB\n")
			isolatedSaveAssertBytes(t, a, wantA)
			if kind == "symlink-to-B" {
				if info, err := os.Lstat(a); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("A.md is no longer the planted symlink: %v, %v", info, err)
				}
			}
		})
	}
}

// TestIsolatedExpectFileMatchBinds: with both identities matching, the pane
// opens and binds A itself, and an edit + Save writes A and never B.
func TestIsolatedExpectFileMatchBinds(t *testing.T) {
	isolatedReceiverEnvironment(t)
	a, b, parentID, fileID := isolatedExpectFilePair(t)
	app, err := NewIsolatedSingleFileAtExpecting(a, 1, 2, parentID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	tab := app.activeTabPtr()
	if !app.isolated || len(app.tabs) != 1 || tab.Path != a || tab.Cursor != posAt(0, 1) {
		t.Fatalf("expected pair did not open A at the location: %+v", tab)
	}
	if got, err := tab.BoundParentID(); err != nil || got.String() != parentID {
		t.Fatalf("bound parent = %v, %v; want %s", got, err, parentID)
	}
	if got, err := tab.BoundFileID(); err != nil || got.String() != fileID {
		t.Fatalf("bound file = %v, %v; want %s", got, err, fileID)
	}
	if app.expect != nil {
		t.Fatal("expectation leaked past construction into later navigation")
	}
	tab.InsertString("X")
	isolatedPublisherMenu(t, app, "Save")
	isolatedSaveAssertBytes(t, a, "AXAA\n")
	isolatedSaveAssertBytes(t, b, "BBB\n")
}

// TestIsolatedExpectFileMalformedRejected: either identity malformed fails
// before any screen or tab exists.
func TestIsolatedExpectFileMalformedRejected(t *testing.T) {
	isolatedReceiverEnvironment(t)
	a, _, parentID, fileID := isolatedExpectFilePair(t)
	for _, ids := range [][2]string{{parentID, ""}, {parentID, "1:2:3"}, {"", fileID}, {"-1:2", fileID}} {
		if app, err := NewIsolatedSingleFileAtExpecting(a, 1, 1, ids[0], ids[1]); app != nil || err == nil {
			if app != nil {
				app.Close()
			}
			t.Fatalf("malformed pair %q accepted", ids)
		}
	}
	isolatedSaveAssertBytes(t, a, "AAA\n")
}

// TestIsolatedExpectFileErrorNamesFileCheck: a matching parent with a changed
// file is refused by the FILE identity check, not some incidental failure.
func TestIsolatedExpectFileErrorNamesFileCheck(t *testing.T) {
	isolatedReceiverEnvironment(t)
	a, b, parentID, _ := isolatedExpectFilePair(t)
	_, err := NewIsolatedSingleFileAtExpecting(a, 1, 1, parentID, isolatedExpectFileID(t, b))
	if err == nil || !strings.Contains(err.Error(), "file identity") {
		t.Fatalf("error = %v, want a file identity mismatch", err)
	}
}
