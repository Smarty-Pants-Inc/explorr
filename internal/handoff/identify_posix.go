//go:build linux || darwin

// =============================================================================
// File: internal/handoff/identify_posix.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package handoff

import (
	"errors"
	"io"
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

// ackNoFollow makes the receiver's ack create refuse a planted symlink.
const ackNoFollow = unix.O_NOFOLLOW

// fileIDOf is the fstat DEV:INO of a held descriptor.
func fileIDOf(f *os.File) (string, error) {
	if f == nil {
		return "", errors.New("no held descriptor")
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return "", err
	}
	return formatID(uint64(st.Dev), st.Ino), nil
}

// readAck opens path no-follow (and non-blocking, so a planted FIFO cannot
// hang the sender), requires a regular file and returns at most limit+1 bytes.
func readAck(path string, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, errors.New("ack is not a regular file")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// formatID is the contract's decimal DEV:INO, both unsigned 64-bit.
func formatID(dev, ino uint64) string {
	return strconv.FormatUint(dev, 10) + ":" + strconv.FormatUint(ino, 10)
}
