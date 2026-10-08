// =============================================================================
// File: internal/handoff/handoff.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

// Package handoff implements Explorr's side of the cross-language handoff
// contract v3 (#1153): ONE sender step that identifies FILE, HOLDS its parent
// directory and the file itself open, creates a private HANDOFF directory,
// and keeps holding until the receiver acknowledges; and ONE receiver ack
// step. Holding the file's descriptor is what pins FILE_ID: an inode cannot
// be reused while any descriptor keeps it open.
//
// The ack is receiver-authenticated, never a bare path marker: the sender
// draws a fresh 128-bit NONCE per handoff and passes it ONLY in the receiver's
// launch argv (never inside HANDOFF). The receiver, after binding, writes
// "NONCE FILE_ID\n" (FILE_ID of the descriptor it bound) into HANDOFF/ack;
// the sender accepts it only if it equals its own nonce and the fstat DEV:INO
// of the descriptor it holds. A pre-created, empty, wrong-nonce or
// other-inode ack is treated exactly like no ack.
package handoff

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// AckName is the file a receiver creates inside HANDOFF once it has
	// bound and checked FILE.
	AckName = "ack"
	// ClosedSuffix is appended to HANDOFF when the sender invalidates it.
	ClosedSuffix = ".closed"
	// DirPrefix names every HANDOFF directory under the system temp dir.
	DirPrefix = "explorr-handoff-"
	// DefaultAckTimeout and DefaultPollInterval are the contract's 15 s / 20 ms.
	DefaultAckTimeout   = 15 * time.Second
	DefaultPollInterval = 20 * time.Millisecond
	// NonceBytes is the per-handoff secret's size (128 bits), sent as 32
	// lowercase hex digits.
	NonceBytes = 16
	// maxAckSize bounds how much of an ack the sender reads.
	maxAckSize = 256
)

// ErrNotConfirmed is reported when no ack existed when HANDOFF was invalidated.
var ErrNotConfirmed = errors.New("the editor did not confirm it opened the file")

var (
	ackTimeout   atomic.Int64
	pollInterval atomic.Int64
)

func init() {
	ackTimeout.Store(int64(DefaultAckTimeout))
	pollInterval.Store(int64(DefaultPollInterval))
}

// SetTiming overrides the ack wait (tests use short timeouts so a refused
// receiver does not cost 15 s). It returns a function restoring the previous
// values. Non-positive arguments keep the current value.
func SetTiming(timeout, poll time.Duration) (restore func()) {
	prevTimeout, prevPoll := ackTimeout.Load(), pollInterval.Load()
	if timeout > 0 {
		ackTimeout.Store(int64(timeout))
	}
	if poll > 0 {
		pollInterval.Store(int64(poll))
	}
	return func() {
		ackTimeout.Store(prevTimeout)
		pollInterval.Store(prevPoll)
	}
}

// CheckPath requires an absolute, normalized path (no ".", "..", "//" or
// trailing "/"). It never consults the filesystem: after the sender's single
// resolution step NOBODY resolves FILE again.
func CheckPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return fmt.Errorf("path %q must be absolute and normalized", path)
	}
	return nil
}

// Sender is one launch's held identity: FILE, the DEV:INO of its parent and
// of itself, the HANDOFF directory, and the two descriptors that pin both
// objects until Await or Abort releases them.
type Sender struct {
	File     string
	ParentID string
	FileID   string
	Dir      string
	// Nonce is this handoff's secret; launch passes it to the receiver's argv
	// only. It is never written into Dir by the sender.
	Nonce string

	mu       sync.Mutex
	parent   *os.File
	file     *os.File
	finished bool
}

// Identify performs sender steps 2 and 3 for an already resolved FILE: open
// dirname(FILE) as a directory, openat(basename) no-follow and require a
// regular file, record both identities, HOLD both descriptors, and create
// HANDOFF (mkdtemp under the system temp dir, mode 0700). FILE itself is not
// resolved here; it must already be absolute and normalized.
func Identify(file string) (*Sender, error) {
	if err := CheckPath(file); err != nil {
		return nil, err
	}
	var raw [NonceBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("handoff nonce: %w", err)
	}
	nonce := hex.EncodeToString(raw[:])
	parent, held, parentID, fileID, err := identify(file)
	if err != nil {
		return nil, fmt.Errorf("identify %s: %w", file, err)
	}
	tmp, err := filepath.Abs(os.TempDir())
	if err == nil {
		var dir string
		dir, err = os.MkdirTemp(tmp, DirPrefix)
		if err == nil {
			if err = os.Chmod(dir, 0o700); err == nil {
				return &Sender{File: file, ParentID: parentID, FileID: fileID, Dir: dir, Nonce: nonce, parent: parent, file: held}, nil
			}
			_ = os.Remove(dir)
		}
	}
	_ = held.Close()
	_ = parent.Close()
	return nil, fmt.Errorf("create handoff directory: %w", err)
}

// Await is sender steps 5 and 6: poll for a VALID HANDOFF/ack (see
// validAck), then ALWAYS invalidate (rename HANDOFF to HANDOFF.closed), close
// both held descriptors, and remove the .closed directory. It returns
// ErrNotConfirmed unless a valid ack existed when HANDOFF was invalidated; an
// invalid ack is never accepted, so the timeout path applies.
func (s *Sender) Await() error {
	deadline := time.Now().Add(time.Duration(ackTimeout.Load()))
	ack := filepath.Join(s.Dir, AckName)
	for {
		if s.validAck(ack) || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Duration(pollInterval.Load()))
	}
	acked, err := s.finish()
	if err != nil {
		return err
	}
	if !acked {
		return ErrNotConfirmed
	}
	return nil
}

// Abort runs the same three close steps after a launch failure and returns
// cause (joined with any cleanup failure). A late receiver can no longer ack.
func (s *Sender) Abort(cause error) error {
	_, err := s.finish()
	return errors.Join(cause, err)
}

// validAck reports whether path is a regular, non-symlink ack whose content
// is exactly AckContent(s.Nonce, id) for id the fstat DEV:INO of the file
// descriptor the sender still holds (which must also be s.FileID). Call it
// only while the descriptor is held.
func (s *Sender) validAck(path string) bool {
	held, err := fileIDOf(s.file)
	if err != nil || held != s.FileID {
		return false
	}
	got, err := readAck(path, maxAckSize)
	if err != nil {
		return false
	}
	want := []byte(AckContent(s.Nonce, held))
	return len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
}

// AckContent is the receiver-authenticated ack body: the sender's nonce and
// the DEV:INO of the file the receiver bound.
func AckContent(nonce, fileID string) string {
	return nonce + " " + fileID + "\n"
}

// CheckNonce requires exactly NonceBytes as lowercase hex.
func CheckNonce(nonce string) error {
	raw, err := hex.DecodeString(nonce)
	if err != nil || len(raw) != NonceBytes || hex.EncodeToString(raw) != nonce {
		return fmt.Errorf("handoff nonce must be %d lowercase hex digits", 2*NonceBytes)
	}
	return nil
}

// finish is step 6, in the contract's fixed order: (a) rename, (b) close the
// held descriptors, (c) remove. acked reports whether a VALID ack existed
// BEFORE the rename (it is read from the renamed directory, while the file
// descriptor it is checked against is still held). If the rename fails,
// HANDOFF is removed instead before the descriptors close, so no late
// receiver can ack once the inode may be reused.
func (s *Sender) finish() (acked bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return false, errors.New("handoff already finished")
	}
	s.finished = true
	closed := s.Dir + ClosedSuffix
	renameErr := os.Rename(s.Dir, closed)
	if renameErr == nil {
		acked = s.validAck(filepath.Join(closed, AckName))
	} else {
		renameErr = errors.Join(fmt.Errorf("invalidate handoff: %w", renameErr), os.RemoveAll(s.Dir))
		closed = ""
	}
	closeErr := errors.Join(s.file.Close(), s.parent.Close())
	var removeErr error
	if closed != "" {
		removeErr = os.RemoveAll(closed)
	}
	if renameErr != nil {
		return false, errors.Join(renameErr, closeErr, removeErr)
	}
	// Close/remove failures cannot widen the window (HANDOFF is already
	// invalid), so they are reported only when nothing else is.
	return acked, errors.Join(closeErr, removeErr)
}

// Send is the whole sender route for one resolved FILE: Identify, launch
// (which must pass File, ParentID, FileID and Dir to the receiver), then Await.
// A launch failure runs the same close steps and returns the launch error.
func Send(file string, launch func(*Sender) error) error {
	s, err := Identify(file)
	if err != nil {
		return err
	}
	if err := launch(s); err != nil {
		return s.Abort(err)
	}
	return s.Await()
}

// Acknowledge is the receiver's step 4: create HANDOFF/ack by path with
// O_CREAT|O_EXCL|O_WRONLY|O_NOFOLLOW and mode 0600, containing
// AckContent(nonce, boundFileID). nonce is the one from the receiver's own
// argv; boundFileID is the DEV:INO of the descriptor it bound. Call it only
// after FILE is bound and both identities checked; on error the receiver must
// refuse. A HANDOFF the sender already invalidated (renamed or removed), or
// an ack someone else pre-created, makes this fail.
func Acknowledge(dir, nonce, boundFileID string) error {
	if err := CheckPath(dir); err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	if err := CheckNonce(nonce); err != nil {
		return err
	}
	if boundFileID == "" || bytes.ContainsAny([]byte(boundFileID), " \n") {
		return errors.New("acknowledge handoff: invalid bound file identity")
	}
	f, err := os.OpenFile(filepath.Join(dir, AckName), os.O_CREATE|os.O_EXCL|os.O_WRONLY|ackNoFollow, 0o600)
	if err != nil {
		return fmt.Errorf("acknowledge handoff: %w", err)
	}
	// One write: the sender rejects a partial ack and keeps polling.
	_, werr := f.WriteString(AckContent(nonce, boundFileID))
	if err := errors.Join(werr, f.Close()); err != nil {
		return fmt.Errorf("acknowledge handoff: %w", err)
	}
	return nil
}
