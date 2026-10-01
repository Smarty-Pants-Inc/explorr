// =============================================================================
// File: internal/editor/secure_save_parentid_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package editor

import (
	"path/filepath"
	"testing"
)

// TestParseParentID pins the shared ^[0-9]+:[0-9]+$ contract with unsigned
// 64-bit fields; anything else is refused rather than normalised.
func TestParseParentID(t *testing.T) {
	for in, want := range map[string]ParentID{
		"0:0":                     {},
		"2049:131":                {Dev: 2049, Ino: 131},
		"007:1":                   {Dev: 7, Ino: 1},
		"18446744073709551615:42": {Dev: 1<<64 - 1, Ino: 42},
	} {
		got, err := ParseParentID(in)
		if err != nil || got != want {
			t.Errorf("ParseParentID(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", ":", "1:", ":1", "1", "1:2:3", "-1:2", "+1:2", "1:-2", " 1:2", "1:2 ", "1_0:2",
		"0x1:2", "1:2\n", "18446744073709551616:1", "1:18446744073709551616", "١:٢",
	} {
		if got, err := ParseParentID(in); err == nil {
			t.Errorf("ParseParentID(%q) accepted as %+v", in, got)
		}
	}
	if s := (ParentID{Dev: 1<<64 - 1, Ino: 9}).String(); s != "18446744073709551615:9" {
		t.Errorf("String = %q", s)
	}
}

// TestBoundParentIDRequiresBinding: ordinary and failed-bind tabs have no held
// parent or original, so they can never satisfy an expected identity.
func TestBoundParentIDRequiresBinding(t *testing.T) {
	tab, err := NewTab(filepath.Join(t.TempDir(), "missing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if id, err := tab.BoundParentID(); err == nil {
		t.Fatalf("unbound tab reported parent %v", id)
	}
	if id, err := tab.BoundFileID(); err == nil {
		t.Fatalf("unbound tab reported file %v", id)
	}
	_ = tab.BindOriginal() // fails: nothing was loaded
	if id, err := tab.BoundParentID(); err == nil {
		t.Fatalf("failed binding reported parent %v", id)
	}
	if id, err := tab.BoundFileID(); err == nil {
		t.Fatalf("failed binding reported file %v", id)
	}
}
