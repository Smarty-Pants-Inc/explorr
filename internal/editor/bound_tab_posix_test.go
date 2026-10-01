//go:build linux || darwin

// =============================================================================
// File: internal/editor/bound_tab_posix_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package editor

import (
	"image"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// lstatParentID is path's own DEV:INO.
func lstatParentID(t *testing.T, path string) ParentID {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return ParentID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}
}

// TestNewBoundTabLoadsCheckedObject: matching IDs load and bind exactly A.
func TestNewBoundTabLoadsCheckedObject(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "A.go")
	if err := os.WriteFile(a, []byte("AAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := NewBoundTab(a, lstatParentID(t, dir), lstatParentID(t, a))
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close()
	if got, _ := tab.BoundFileID(); got != lstatParentID(t, a) || tab.Buffer.String() != "AAA\n" {
		t.Fatalf("bound %v %q", got, tab.Buffer.String())
	}
}

// TestNewBoundTabImagePreview: a matching image opens as a read-only preview
// decoded from the checked bytes; a swap refuses; it never reloads or saves.
func TestNewBoundTabImagePreview(t *testing.T) {
	dir := t.TempDir()
	pic := filepath.Join(dir, "pic.png")
	other := filepath.Join(dir, "other.png")
	writePNG(t, pic, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	writePNG(t, other, image.NewRGBA(image.Rect(0, 0, 7, 7)))
	pid, fid := lstatParentID(t, dir), lstatParentID(t, pic)
	tab, err := NewBoundTab(pic, pid, fid)
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close()
	if !tab.IsImage() || tab.Image.Bounds().Dx() != 3 || tab.ImageFmt != "png" {
		t.Fatalf("preview = image:%v bounds:%v fmt:%q", tab.IsImage(), tab.Image.Bounds(), tab.ImageFmt)
	}
	// Swapping the name afterwards cannot change the preview, and it never saves.
	if err := os.Remove(pic); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.png", pic); err != nil {
		t.Fatal(err)
	}
	if err := tab.Reload(); err == nil || tab.Image.Bounds().Dx() != 3 {
		t.Fatalf("bound preview reloaded through its path: err=%v bounds=%v", err, tab.Image.Bounds())
	}
	if err := tab.Save(); err == nil {
		t.Fatal("image preview saved")
	}
	// A symlink at the name at open time is refused, as is a wrong FILE_ID.
	if swapped, err := NewBoundTab(pic, pid, fid); err == nil {
		swapped.Close()
		t.Fatal("symlinked image accepted")
	}
	if wrong, err := NewBoundTab(other, pid, fid); err == nil {
		wrong.Close()
		t.Fatal("image with another FILE_ID accepted")
	}
}

// TestNewBoundTabRefuses: symlinks, FIFOs (without hanging), mismatched IDs,
// undecodable images and unnormalized paths are refused.
func TestNewBoundTabRefuses(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "A.go")
	b := filepath.Join(dir, "B.go")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink("A.go", link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.go")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	png := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(png, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid := lstatParentID(t, dir)
	for name, tc := range map[string]struct {
		path   string
		parent ParentID
		file   ParentID
	}{
		"symlink":      {link, pid, lstatParentID(t, a)},
		"fifo":         {fifo, pid, lstatParentID(t, fifo)},
		"wrong file":   {a, pid, lstatParentID(t, b)},
		"wrong parent": {a, lstatParentID(t, a), lstatParentID(t, a)},
		"undecodable":  {png, pid, lstatParentID(t, png)},
		"unnormalized": {dir + "/./A.go", pid, lstatParentID(t, a)},
		"relative":     {"A.go", pid, lstatParentID(t, a)},
	} {
		if tab, err := NewBoundTab(tc.path, tc.parent, tc.file); err == nil {
			tab.Close()
			t.Errorf("%s accepted", name)
		}
	}
}
