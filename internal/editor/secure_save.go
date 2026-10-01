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
	"strconv"
	"strings"
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

// ParentID is a directory identity in the cross-language DEV:INO contract
// shared with Reviewr and its edit helper: decimal, both fields unsigned 64-bit
// (Go uint64(st.Dev), uint64(st.Ino)).
type ParentID struct {
	Dev uint64
	Ino uint64
}

// String formats the identity as decimal DEV:INO.
func (p ParentID) String() string {
	return strconv.FormatUint(p.Dev, 10) + ":" + strconv.FormatUint(p.Ino, 10)
}

// ParseParentID accepts exactly ^[0-9]+:[0-9]+$ with each field fitting in
// uint64. Signs, spaces, prefixes and overflow are rejected, never normalised.
func ParseParentID(s string) (ParentID, error) {
	dev, ino, ok := strings.Cut(s, ":")
	if !ok || !parentIDDigits(dev) || !parentIDDigits(ino) {
		return ParentID{}, fmt.Errorf("invalid parent identity %q: want DEV:INO", s)
	}
	d, err := strconv.ParseUint(dev, 10, 64)
	if err != nil {
		return ParentID{}, fmt.Errorf("invalid parent identity %q: %w", s, err)
	}
	i, err := strconv.ParseUint(ino, 10, 64)
	if err != nil {
		return ParentID{}, fmt.Errorf("invalid parent identity %q: %w", s, err)
	}
	return ParentID{Dev: d, Ino: i}, nil
}

// parentIDDigits reports whether s is a non-empty run of ASCII digits.
func parentIDDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// BoundParentID returns the identity of the parent directory this tab HOLDS
// open (fstat at bind time), not a fresh pathname lookup. Unbound, failed or
// closed bindings, and unsupported platforms, return an error: callers that
// expect a parent must fail closed.
func (t *Tab) BoundParentID() (ParentID, error) {
	if !t.originalBound || t.original == nil {
		return ParentID{}, fmt.Errorf("tab has no bound original parent")
	}
	if t.original.closed {
		return ParentID{}, fmt.Errorf("original binding is closed")
	}
	return t.original.parentID()
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
