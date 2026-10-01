//go:build linux || darwin

// =============================================================================
// File: internal/handoff/identify_posix.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// identify opens dirname(file) as a directory and basename(file) relative to
// it with O_NOFOLLOW|O_NONBLOCK, requires a regular file, and returns both
// descriptors (to be HELD) with their fstat DEV:INO.
func identify(file string) (parent, held *os.File, parentID, fileID string, err error) {
	dir := filepath.Dir(file)
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, "", "", err
	}
	parent = os.NewFile(uintptr(dfd), dir)
	var dst unix.Stat_t
	if err := unix.Fstat(dfd, &dst); err != nil {
		parent.Close()
		return nil, nil, "", "", err
	}
	ffd, err := unix.Openat(dfd, filepath.Base(file), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		parent.Close()
		return nil, nil, "", "", err
	}
	held = os.NewFile(uintptr(ffd), file)
	var fst unix.Stat_t
	if err := unix.Fstat(ffd, &fst); err != nil {
		held.Close()
		parent.Close()
		return nil, nil, "", "", err
	}
	if fst.Mode&unix.S_IFMT != unix.S_IFREG {
		held.Close()
		parent.Close()
		return nil, nil, "", "", errors.New("not a regular file")
	}
	return parent, held, formatID(uint64(dst.Dev), dst.Ino), formatID(uint64(fst.Dev), fst.Ino), nil
}

// formatID is the contract's decimal DEV:INO, both unsigned 64-bit.
func formatID(dev, ino uint64) string {
	return strconv.FormatUint(dev, 10) + ":" + strconv.FormatUint(ino, 10)
}
