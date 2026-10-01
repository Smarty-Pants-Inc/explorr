//go:build linux || darwin

// =============================================================================
// File: internal/editor/secure_save_posix.go
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// bindOriginal rejects a changed/symlink entry before retaining either handle.
// Comparing the securely read bytes also detects changes between load and bind.
func bindOriginal(path string, loaded os.FileInfo, contents string) (*boundOriginal, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Dir(abs), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(fd), filepath.Dir(abs))
	parentInfo, err := parent.Stat()
	if err != nil {
		parent.Close()
		return nil, err
	}
	b := &boundOriginal{path: abs, name: filepath.Base(abs), parent: parent, parentInfo: parentInfo}
	f, info, err := b.openEntry(unix.O_RDONLY)
	if err != nil {
		parent.Close()
		return nil, err
	}
	if !os.SameFile(loaded, info) {
		err = fmt.Errorf("original changed since loading")
	} else if err = b.checkParent(path); err == nil {
		data, readErr := io.ReadAll(f)
		if readErr != nil {
			err = readErr
		} else if string(data) != contents {
			err = fmt.Errorf("original contents changed since loading")
		} else {
			err = b.checkParent(path)
		}
	}
	if err != nil {
		f.Close()
		parent.Close()
		return nil, err
	}
	b.original, b.originalInfo = f, info
	return b, nil
}

// checkParent requires the displayed path to still name the held parent object.
// Ancestor symlinks already present on load (e.g. macOS /tmp) are allowed, but
// redirecting any ancestor to a different directory fails. Writes never use it.
func (b *boundOriginal) checkParent(path string) error {
	if b.closed {
		return fmt.Errorf("original binding is closed")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if abs != b.path {
		return fmt.Errorf("bound original path cannot change")
	}
	info, err := os.Stat(filepath.Dir(abs))
	if err != nil {
		return err
	}
	if !os.SameFile(b.parentInfo, info) {
		return fmt.Errorf("original parent directory changed")
	}
	return nil
}

// openEntry never follows a final symlink, creates a file, or truncates it.
// NONBLOCK prevents a malicious FIFO replacement from hanging the editor.
func (b *boundOriginal) openEntry(flags int) (*os.File, os.FileInfo, error) {
	if b.closed {
		return nil, nil, fmt.Errorf("original binding is closed")
	}
	fd, err := unix.Openat(int(b.parent.Fd()), b.name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), b.path)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("original entry is not a regular file")
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// save opens without truncation and validates BEFORE the first mutation. Even
// if the namespace changes afterwards, this descriptor can only write the
// pinned original inode, never a replacement or a symlink's target.
func (b *boundOriginal) save(path string, data []byte) (os.FileInfo, error) {
	if err := b.checkParent(path); err != nil {
		return nil, err
	}
	f, info, err := b.openEntry(unix.O_WRONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !os.SameFile(b.originalInfo, info) {
		return nil, fmt.Errorf("original file identity changed; reload a clean tab first")
	}
	if err := b.checkParent(path); err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		return nil, err
	}
	if err := f.Truncate(int64(len(data))); err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return info, nil
}

// reload permits atomic regular-file replacement only for a clean tab and
// within the unchanged parent. Failures leave both the buffer and binding intact.
func (b *boundOriginal) reload(path string, allowRebind bool) ([]byte, os.FileInfo, error) {
	if err := b.checkParent(path); err != nil {
		return nil, nil, err
	}
	f, info, err := b.openEntry(unix.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	keep := false
	defer func() {
		if !keep {
			f.Close()
		}
	}()
	if !allowRebind && !os.SameFile(b.originalInfo, info) {
		return nil, nil, fmt.Errorf("cannot rebind a dirty original tab")
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	info, err = f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if err := b.checkParent(path); err != nil {
		return nil, nil, err
	}
	b.original.Close()
	b.original, b.originalInfo = f, info
	keep = true
	return data, info, nil
}

// parentID reads DEV:INO from the held parent's FileInfo, captured by fstat on
// the retained descriptor. uint64(st.Dev) is the shared contract's formatting.
func (b *boundOriginal) parentID() (ParentID, error) {
	st, ok := b.parentInfo.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return ParentID{}, fmt.Errorf("parent identity is unavailable")
	}
	return ParentID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil
}
