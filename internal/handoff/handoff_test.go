//go:build linux || darwin

// =============================================================================
// File: internal/handoff/handoff_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// lstatID is the contract's DEV:INO of path itself.
func lstatID(t *testing.T, path string) string {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(uint64(st.Ino), 10)
}

func fastTiming(t *testing.T) {
	t.Helper()
	t.Cleanup(SetTiming(200*time.Millisecond, 5*time.Millisecond))
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestIdentifyHoldsRegularFile: IDs match the objects, HANDOFF is a private
// mkdtemp directory under the system temp dir.
func TestIdentifyHoldsRegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "A.go")
	writeFile(t, file, "A")
	s, err := Identify(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort(nil)
	if s.File != file || s.ParentID != lstatID(t, dir) || s.FileID != lstatID(t, file) {
		t.Fatalf("sender = %+v", s)
	}
	info, err := os.Lstat(s.Dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("HANDOFF %s: %v %v", s.Dir, info, err)
	}
	tmp, _ := filepath.Abs(os.TempDir())
	if filepath.Dir(s.Dir) != tmp || !strings.HasPrefix(filepath.Base(s.Dir), DirPrefix) {
		t.Fatalf("HANDOFF %s not mkdtemp'd under %s", s.Dir, tmp)
	}
}

// TestIdentifyRefuses: unnormalized/relative names, symlinks, FIFOs (without
// hanging) and directories are refused, and no HANDOFF is left behind.
func TestIdentifyRefuses(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	writeFile(t, target, "T")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := filepath.Glob(filepath.Join(os.TempDir(), DirPrefix+"*"))
	for _, p := range []string{"", "rel/target", dir + "/./target", dir + "//target", dir + "/sub/../target", target + "/", link, fifo, dir, filepath.Join(dir, "missing")} {
		if s, err := Identify(p); err == nil {
			s.Abort(nil)
			t.Errorf("Identify(%q) accepted", p)
		}
	}
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), DirPrefix+"*"))
	if len(after) > len(before) {
		t.Fatalf("refusals leaked HANDOFF directories: %v", after)
	}
}

// TestSendAckCompletes: a receiver that acks lets Send return nil, and both
// HANDOFF and HANDOFF.closed are gone afterwards.
func TestSendAckCompletes(t *testing.T) {
	fastTiming(t)
	file := filepath.Join(t.TempDir(), "A")
	writeFile(t, file, "A")
	var dir string
	err := Send(file, func(s *Sender) error {
		dir = s.Dir
		go func() {
			time.Sleep(30 * time.Millisecond)
			if err := Acknowledge(s.Dir); err != nil {
				t.Error(err)
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertGone(t, dir)
}

func assertGone(t *testing.T, dir string) {
	t.Helper()
	for _, p := range []string{dir, dir + ClosedSuffix} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", p, err)
		}
	}
}

// TestSendWithoutAckReportsError: timeout runs the close steps and reports.
func TestSendWithoutAckReportsError(t *testing.T) {
	fastTiming(t)
	file := filepath.Join(t.TempDir(), "A")
	writeFile(t, file, "A")
	var dir string
	err := Send(file, func(s *Sender) error { dir = s.Dir; return nil })
	if !errors.Is(err, ErrNotConfirmed) || err.Error() != "the editor did not confirm it opened the file" {
		t.Fatalf("err = %v", err)
	}
	assertGone(t, dir)
	if err := Acknowledge(dir); err == nil {
		t.Fatal("ack after invalidation succeeded")
	}
}

// TestSendLaunchFailureRunsCloseSteps: the launch error is returned and the
// HANDOFF is invalidated, so a late receiver cannot ack.
func TestSendLaunchFailureRunsCloseSteps(t *testing.T) {
	fastTiming(t)
	file := filepath.Join(t.TempDir(), "A")
	writeFile(t, file, "A")
	var sender *Sender
	boom := errors.New("launch failed")
	err := Send(file, func(s *Sender) error { sender = s; return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	assertGone(t, sender.Dir)
	if err := Acknowledge(sender.Dir); err == nil {
		t.Fatal("late ack succeeded")
	}
	if _, err := sender.file.Stat(); err == nil {
		t.Fatal("held file descriptor still open after launch failure")
	}
	if _, err := sender.parent.Stat(); err == nil {
		t.Fatal("held parent descriptor still open after launch failure")
	}
	if err := sender.Abort(nil); err == nil {
		t.Fatal("second finish was accepted")
	}
}

// TestAcknowledgeIsExclusivePrivate: ack is created 0600, never twice, only
// under an absolute normalized HANDOFF.
func TestAcknowledgeIsExclusivePrivate(t *testing.T) {
	dir := t.TempDir()
	if err := Acknowledge(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dir, AckName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("ack = %v, %v", info, err)
	}
	if err := Acknowledge(dir); err == nil {
		t.Fatal("second ack succeeded")
	}
	for _, bad := range []string{"rel", dir + "/", dir + "/.", filepath.Join(dir, "missing")} {
		if err := Acknowledge(bad); err == nil {
			t.Errorf("Acknowledge(%q) accepted", bad)
		}
	}
}

// TestHoldPreventsInodeReuse: while the sender holds A, unlinking and
// recreating A never yields A's FILE_ID again. The control (no hold) only
// logs whether this filesystem reuses inode numbers.
func TestHoldPreventsInodeReuse(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "A")
	writeFile(t, a, "A")
	s, err := Identify(a)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort(nil)
	for i := 0; i < 20; i++ {
		if err := os.Remove(a); err != nil {
			t.Fatal(err)
		}
		writeFile(t, a, "new")
		if got := lstatID(t, a); got == s.FileID {
			t.Fatalf("held inode %s was reused by a new A", got)
		}
	}
	control := filepath.Join(dir, "C")
	writeFile(t, control, "C")
	old := lstatID(t, control)
	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
	writeFile(t, control, "C2")
	t.Logf("control without hold: reuse=%v (%s -> %s)", lstatID(t, control) == old, old, lstatID(t, control))
}
