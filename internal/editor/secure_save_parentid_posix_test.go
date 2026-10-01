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
