//go:build linux || darwin

// =============================================================================
// File: internal/editor/secure_save_parentid_posix_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// secureSaveStatID formats a directory's identity exactly as the contract does
// (Go: uint64(st.Dev), uint64(st.Ino)).
func secureSaveStatID(t *testing.T, dir string) ParentID {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		t.Fatal(err)
	}
	return ParentID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}
}

// TestBoundParentIDReportsHeldParent: the identity is the HELD parent's, so it
// keeps naming the original directory after the pathname parent is replaced,
// and is unavailable once the binding is closed.
func TestBoundParentIDReportsHeldParent(t *testing.T) {
	tab, pathA, _ := secureSaveFiles(t)
	parent := filepath.Dir(pathA)
	want := secureSaveStatID(t, parent)
	got, err := tab.BoundParentID()
	if err != nil || got != want {
		t.Fatalf("BoundParentID = %v, %v; want %v", got, err, want)
	}
	if parsed, err := ParseParentID(got.String()); err != nil || parsed != want {
		t.Fatalf("round trip %q = %v, %v", got.String(), parsed, err)
	}
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if replaced := secureSaveStatID(t, parent); replaced == want {
		t.Fatal("replacement directory reused the identity")
	}
	if got, err := tab.BoundParentID(); err != nil || got != want {
		t.Fatalf("held identity changed with the namespace: %v, %v", got, err)
	}
	if err := tab.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := tab.BoundParentID(); err == nil {
		t.Fatalf("closed binding reported %v", got)
	}
}

// secureSaveLstatID is a pathname's own identity (no final-symlink following),
// as Reviewr's no-follow openat+fstat reports it.
func secureSaveLstatID(t *testing.T, path string) ParentID {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return ParentID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}
}

// TestBoundFileIDReportsHeldOriginal: the identity is the HELD original's, so
// it keeps naming A after A's name is redirected, and is unavailable once the
// binding is closed.
func TestBoundFileIDReportsHeldOriginal(t *testing.T) {
	tab, pathA, pathB := secureSaveFiles(t)
	want := secureSaveLstatID(t, pathA)
	got, err := tab.BoundFileID()
	if err != nil || got != want {
		t.Fatalf("BoundFileID = %v, %v; want %v", got, err, want)
	}
	if err := os.Remove(pathA); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pathB, pathA); err != nil {
		t.Fatal(err)
	}
	if got, err := tab.BoundFileID(); err != nil || got != want {
		t.Fatalf("held identity changed with the namespace: %v, %v", got, err)
	}
	if err := tab.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := tab.BoundFileID(); err == nil {
		t.Fatalf("closed binding reported %v", got)
	}
}

// TestBindOriginalRefusesSymlinkAtLaunch pins the launch-time half of the
// contract: NewTab follows a symlinked FILE (loading its target), but
// BindOriginal's O_NOFOLLOW openat of the basename refuses the link itself, so
// a FILE that is already a symlink never binds and has no file identity.
func TestBindOriginalRefusesSymlinkAtLaunch(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "A.md"), filepath.Join(dir, "B.md")
	secureSaveWrite(t, b, "untouched B")
	if err := os.Symlink("B.md", a); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(a)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Buffer.String() != "untouched B" {
		t.Fatalf("NewTab did not follow the link: %q", tab.Buffer.String())
	}
	if err := tab.BindOriginal(); err == nil {
		t.Fatal("symlinked FILE bound")
	}
	if id, err := tab.BoundFileID(); err == nil {
		t.Fatalf("failed binding reported file %v", id)
	}
	tab.InsertString("X")
	if err := tab.Save(); err == nil {
		t.Fatal("failed binding still saved")
	}
	if got, err := os.ReadFile(b); err != nil || string(got) != "untouched B" {
		t.Fatalf("B changed: %q, %v", got, err)
	}
}
