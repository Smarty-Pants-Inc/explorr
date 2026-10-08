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
			if err := Acknowledge(s.Dir, s.Nonce, s.FileID); err != nil {
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
	var dir, nonce, id string
	start := time.Now()
	err := Send(file, func(s *Sender) error { dir, nonce, id = s.Dir, s.Nonce, s.FileID; return nil })
	if !errors.Is(err, ErrNotConfirmed) || err.Error() != "the editor did not confirm it opened the file" {
		t.Fatalf("err = %v", err)
	}
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Fatalf("sender gave up after %v, before the ack timeout", waited)
	}
	assertGone(t, dir)
	if err := Acknowledge(dir, nonce, id); err == nil {
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
	if err := Acknowledge(sender.Dir, sender.Nonce, sender.FileID); err == nil {
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

// testNonce is a well-formed nonce no real sender drew.
const testNonce = "00112233445566778899aabbccddeeff"

// TestAcknowledgeIsExclusivePrivate: ack is created 0600 with exactly
// "NONCE FILE_ID\n", never twice, never through a planted symlink, only under
// an absolute normalized HANDOFF and only with a well-formed nonce.
func TestAcknowledgeIsExclusivePrivate(t *testing.T) {
	dir := t.TempDir()
	if err := Acknowledge(dir, testNonce, "1:2"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dir, AckName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("ack = %v, %v", info, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, AckName)); string(data) != testNonce+" 1:2\n" {
		t.Fatalf("ack content = %q", data)
	}
	if err := Acknowledge(dir, testNonce, "1:2"); err == nil {
		t.Fatal("second ack succeeded")
	}
	for _, bad := range []string{"rel", dir + "/", dir + "/.", filepath.Join(dir, "missing")} {
		if err := Acknowledge(bad, testNonce, "1:2"); err == nil {
			t.Errorf("Acknowledge(%q) accepted", bad)
		}
	}
	other := t.TempDir()
	for _, nonce := range []string{"", "00", strings.ToUpper(testNonce), testNonce + "00", "zz" + testNonce[2:]} {
		if err := Acknowledge(other, nonce, "1:2"); err == nil {
			t.Errorf("nonce %q accepted", nonce)
		}
	}
	if err := Acknowledge(other, testNonce, ""); err == nil {
		t.Error("empty bound identity accepted")
	}
	victim := filepath.Join(t.TempDir(), "victim")
	writeFile(t, victim, "V")
	if err := os.Symlink(victim, filepath.Join(other, AckName)); err != nil {
		t.Fatal(err)
	}
	if err := Acknowledge(other, testNonce, "1:2"); err == nil {
		t.Fatal("ack through a planted symlink succeeded")
	}
	if data, _ := os.ReadFile(victim); string(data) != "V" {
		t.Fatalf("planted symlink target written: %q", data)
	}
}

// TestNoncePerHandoff: every handoff draws a fresh well-formed 128-bit nonce.
func TestNoncePerHandoff(t *testing.T) {
	file := filepath.Join(t.TempDir(), "A")
	writeFile(t, file, "A")
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		s, err := Identify(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckNonce(s.Nonce); err != nil || seen[s.Nonce] {
			t.Fatalf("nonce %q: %v (repeat=%v)", s.Nonce, err, seen[s.Nonce])
		}
		seen[s.Nonce] = true
		entries, _ := os.ReadDir(s.Dir)
		if len(entries) != 0 {
			t.Fatalf("sender wrote %v into HANDOFF", entries)
		}
		_ = s.Abort(nil)
	}
}

// TestSendAcceptsValidAckWrittenBeforeAwait: a receiver that bound the held
// file and acked with the sender's nonce before Await is accepted at once.
func TestSendAcceptsValidAckWrittenBeforeAwait(t *testing.T) {
	t.Cleanup(SetTiming(5*time.Second, 5*time.Millisecond))
	file := filepath.Join(t.TempDir(), "A")
	writeFile(t, file, "A")
	start := time.Now()
	err := Send(file, func(s *Sender) error { return Acknowledge(s.Dir, s.Nonce, lstatID(t, s.File)) })
	if err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("valid ack took %v (treated as missing?)", waited)
	}
}

// TestAwaitRejectsForgedAcks is the round-5 P2: a same-UID process that knows
// HANDOFF can pre-create ack, but without the sender's nonce bound to the held
// file's DEV:INO its ack is treated exactly like no ack (timeout, then
// ErrNotConfirmed), and the genuine receiver's O_EXCL ack then fails closed.
func TestAwaitRejectsForgedAcks(t *testing.T) {
	const otherNonce = "ffeeddccbbaa99887766554433221100"
	cases := map[string]func(t *testing.T, s *Sender, ack string){
		"empty marker": func(t *testing.T, s *Sender, ack string) { writeFile(t, ack, "") },
		"no nonce":     func(t *testing.T, s *Sender, ack string) { writeFile(t, ack, s.FileID+"\n") },
		"wrong nonce":  func(t *testing.T, s *Sender, ack string) { writeFile(t, ack, AckContent(otherNonce, s.FileID)) },
		"right nonce wrong dev:ino": func(t *testing.T, s *Sender, ack string) {
			other := filepath.Join(filepath.Dir(s.File), "B")
			writeFile(t, other, "B")
			writeFile(t, ack, AckContent(s.Nonce, lstatID(t, other)))
		},
		"right nonce trailing junk": func(t *testing.T, s *Sender, ack string) {
			writeFile(t, ack, AckContent(s.Nonce, s.FileID)+"x")
		},
		"symlink to a valid ack": func(t *testing.T, s *Sender, ack string) {
			elsewhere := filepath.Join(t.TempDir(), "ack")
			writeFile(t, elsewhere, AckContent(s.Nonce, s.FileID))
			if err := os.Symlink(elsewhere, ack); err != nil {
				t.Fatal(err)
			}
		},
		"fifo": func(t *testing.T, s *Sender, ack string) {
			if err := syscall.Mkfifo(ack, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, s *Sender, ack string) {
			if err := os.Mkdir(ack, 0o700); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			fastTiming(t)
			file := filepath.Join(t.TempDir(), "A")
			writeFile(t, file, "A")
			var dir string
			start := time.Now()
			err := Send(file, func(s *Sender) error {
				dir = s.Dir
				plant(t, s, filepath.Join(s.Dir, AckName))
				if err := Acknowledge(s.Dir, s.Nonce, s.FileID); err == nil {
					t.Error("genuine receiver acked over a planted ack")
				}
				return nil
			})
			if !errors.Is(err, ErrNotConfirmed) {
				t.Fatalf("forged ack accepted: err = %v", err)
			}
			if waited := time.Since(start); waited < 200*time.Millisecond {
				t.Fatalf("forged ack ended the wait early (%v)", waited)
			}
			assertGone(t, dir)
		})
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
