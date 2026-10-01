// =============================================================================
// File: internal/editor/secure_save.go
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// boundOriginal pins both objects, preventing inode reuse while the tab is open.
// Path checks are deliberately separate from descriptor-relative file access:
// a namespace race can never redirect a write to a different file.
type boundOriginal struct {
	path         string
	name         string
	parent       *os.File
	parentInfo   os.FileInfo
	original     *os.File
	originalInfo os.FileInfo
	closed       bool
}

// BindOriginal opts a loaded, clean text tab into identity-bound saving. Call
// immediately after NewTab, before editing. A failed bind also disables ordinary
// saves; callers must abort the isolated editor rather than fall back to Save.
// Clean Reload may bind an atomic regular-file replacement in the same parent.
// Bound tabs cannot be saved under another path. Close releases their handles.
func (t *Tab) BindOriginal() error {
	if t.originalBound {
		return fmt.Errorf("original binding already requested")
	}
	t.originalBound = true
	if t.Path == "" || t.IsImage() || t.Synthetic || t.Dirty || t.loadedInfo == nil {
		return fmt.Errorf("original binding requires a loaded, clean regular text file")
	}
	original, err := bindOriginal(t.Path, t.loadedInfo, t.Buffer.String())
	if err != nil {
		return fmt.Errorf("bind original: %w", err)
	}
	t.original = original
	return nil
}

// Close releases bound-original handles. It is idempotent and leaves bound
// Save/Reload disabled, never reverting to an unprotected path-based save.
// Ordinary tabs hold no such resources and need no special shutdown handling.
func (t *Tab) Close() error {
	if t.original == nil || t.original.closed {
		return nil
	}
	t.original.closed = true
	return errors.Join(t.original.original.Close(), t.original.parent.Close())
}

// readTabFile captures identity and contents from the SAME opened file. Ordinary
// tabs retain their existing symlink-following behavior; binding is opt-in.
func readTabFile(path string) ([]byte, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	return data, info, err
}
