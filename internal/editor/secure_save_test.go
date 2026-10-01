// =============================================================================
// File: internal/editor/secure_save_test.go
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBindOriginalRequiresLoadedCleanText rejects unsupported tab states before
// any writes and ensures a binding failure is never an ordinary Save fallback.
func TestBindOriginalRequiresLoadedCleanText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "original.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"scratch", "missing", "dirty", "synthetic", "image", "unloaded"} {
		t.Run(kind, func(t *testing.T) {
			tab, err := NewTab(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "scratch":
				tab.Path = ""
			case "missing":
				tab, err = NewTab(filepath.Join(dir, "missing.txt"))
				if err != nil {
					t.Fatal(err)
				}
			case "dirty":
				tab.InsertString("edit ")
			case "synthetic":
				tab.Synthetic = true
			case "image":
				tab.Mode = imageMode
			case "unloaded":
				tab = &Tab{Path: path, Buffer: NewBuffer("original")}
			}
			if err := tab.BindOriginal(); err == nil {
				t.Fatal("invalid tab accepted binding")
			}
			if err := tab.Save(); err == nil {
				t.Fatal("failed binding allowed Save")
			}
			if err := tab.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "original" {
		t.Fatalf("binding failures changed source: %q, %v", got, err)
	}
}

// TestReadTabFileIdentifiesOpenedFile verifies identity is recorded with bytes,
// while a missing ordinary tab still reports the existing missing-file contract.
func TestReadTabFileIdentifiesOpenedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	data, info, err := readTabFile(path)
	if err != nil || string(data) != "original" || info == nil {
		t.Fatalf("readTabFile: %q, %v, %v", data, info, err)
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(info, current) {
		t.Fatalf("wrong loaded identity: %v", err)
	}
	if _, _, err := readTabFile(path + ".missing"); !os.IsNotExist(err) {
		t.Fatalf("missing error = %v", err)
	}
}
