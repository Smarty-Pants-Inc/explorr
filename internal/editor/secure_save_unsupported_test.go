//go:build !linux && !darwin

// =============================================================================
// File: internal/editor/secure_save_unsupported_test.go
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBoundOriginalUnsupportedFailsBeforeWrite exercises the platform contract:
// neither binding failure nor direct platform methods can fall back to writes.
func TestBoundOriginalUnsupportedFailsBeforeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.BindOriginal(); err == nil {
		t.Fatal("unsupported binding succeeded")
	}
	tab.InsertString("edit ")
	if err := tab.Save(); err == nil || !tab.Dirty {
		t.Fatal("unsupported bound Save did not fail closed")
	}
	if err := tab.Reload(); err == nil {
		t.Fatal("unsupported bound Reload did not fail closed")
	}
	b := &boundOriginal{}
	if _, err := b.save(path, []byte("unsafe")); err == nil {
		t.Fatal("unsupported platform save succeeded")
	}
	if _, _, err := b.reload(path, true); err == nil {
		t.Fatal("unsupported platform reload succeeded")
	}
	if id, err := b.parentID(); err == nil {
		t.Fatalf("unsupported platform reported parent %v", id)
	}
	if id, err := b.fileID(); err == nil {
		t.Fatalf("unsupported platform reported file %v", id)
	}
	if id, err := tab.BoundFileID(); err == nil {
		t.Fatalf("unsupported binding reported file %v", id)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "original" {
		t.Fatalf("unsupported binding changed source: %q, %v", got, err)
	}
}
