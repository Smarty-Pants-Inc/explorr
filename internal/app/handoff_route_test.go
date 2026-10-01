//go:build linux || darwin

// =============================================================================
// File: internal/app/handoff_route_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Smarty-Pants-Inc/explorr/internal/handoff"
)

// splitShellWords undoes shellQuote's POSIX quoting for the recorded command.
func splitShellWords(t *testing.T, command string) []string {
	t.Helper()
	var words []string
	var cur strings.Builder
	inWord, quote := false, byte(0)
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == ' ':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 {
		t.Fatalf("unterminated quote in %q", command)
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// receiverArgs maps a recorded `--single-file-at FILE LINE COL --expect-parent P
// --expect-file F --handoff H` launch onto its parts, failing on any other shape.
type receiverArgs struct {
	file              string
	line, col         int
	parent, fileID, h string
}

func parseReceiverCommand(t *testing.T, command string) receiverArgs {
	t.Helper()
	w := splitShellWords(t, command)
	if len(w) != 12 || w[0] != "exec" || w[2] != "--single-file-at" || w[6] != "--expect-parent" || w[8] != "--expect-file" || w[10] != "--handoff" {
		t.Fatalf("receiver command %q (words %q) lacks the identity triple", command, w)
	}
	line, _ := strconv.Atoi(w[4])
	col, _ := strconv.Atoi(w[5])
	return receiverArgs{file: w[3], line: line, col: col, parent: w[7], fileID: w[9], h: w[11]}
}

// handoffSwapFixture is docs/A.txt (the clicked file) plus the victim
// docs/B.txt in the same parent.
type handoffSwapFixture struct {
	dir, a, b string
}

func newHandoffSwapFixture(t *testing.T, ext string) handoffSwapFixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "docs")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := handoffSwapFixture{dir: dir, a: filepath.Join(dir, "A"+ext), b: filepath.Join(dir, "B"+ext)}
	isolatedSaveWrite(t, f.a, "AAA\n")
	isolatedSaveWrite(t, f.b, "BBB\n")
	return f
}

func isolatedSaveWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// swap performs the attack between the sender's identify and the receiver.
// It returns the bytes A's ORIGINAL inode must still hold afterwards.
func (f handoffSwapFixture) swap(t *testing.T, kind string) {
	t.Helper()
	switch kind {
	case "symlink":
		if err := os.Remove(f.a); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Base(f.b), f.a); err != nil {
			t.Fatal(err)
		}
	case "hardlink":
		if err := os.Remove(f.a); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(f.b, f.a); err != nil {
			t.Fatal(err)
		}
	case "parent":
		// The new parent holds a hard link to the ORIGINAL A: only the parent
		// identity distinguishes it.
		old := f.dir + ".old"
		if err := os.Rename(f.dir, old); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(old, filepath.Base(f.a)), f.a); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(old, filepath.Base(f.b)), f.b); err != nil {
			t.Fatal(err)
		}
	case "none":
	default:
		t.Fatalf("unknown swap %q", kind)
	}
}

// assertUntouched: neither A's original bytes nor the victim changed.
func (f handoffSwapFixture) assertUntouched(t *testing.T) {
	t.Helper()
	isolatedSaveAssertBytes(t, f.b, "BBB\n")
	if info, err := os.Lstat(f.a); err == nil && info.Mode().IsRegular() {
		data, _ := os.ReadFile(f.a)
		if s := string(data); s != "AAA\n" && s != "BBB\n" {
			t.Fatalf("A changed to %q", s)
		}
	}
}

// fakeHerdRReceiver records every manager call; `pane run` performs the swap
// and then runs the REAL receiver constructor on the recorded command.
type fakeHerdRReceiver struct {
	t       *testing.T
	kind    string
	fixture handoffSwapFixture
	calls   [][]string
	args    receiverArgs
	app     *App
	err     error
	ran     bool
}

func (f *fakeHerdRReceiver) run(bin string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	switch {
	case len(args) >= 2 && args[0] == "pane" && args[1] == "split":
		return []byte(`{"result":{"pane":{"pane_id":"wA:p8"}}}`), nil
	case len(args) == 4 && args[0] == "pane" && args[1] == "run":
		f.args = parseReceiverCommand(f.t, args[3])
		f.fixture.swap(f.t, f.kind)
		f.ran = true
		f.app, f.err = NewIsolatedSingleFileAtExpecting(f.args.file, f.args.line, f.args.col, f.args.parent, f.args.fileID, f.args.h)
		return []byte(`{"result":{}}`), nil
	}
	return []byte(`{"result":{}}`), nil
}

func installFakeHerdR(t *testing.T, kind string, fixture handoffSwapFixture) *fakeHerdRReceiver {
	t.Helper()
	isolatedReceiverEnvironment(t)
	t.Cleanup(handoff.SetTiming(300*time.Millisecond, 5*time.Millisecond))
	t.Setenv("HERDR_PANE_ID", "wA:p7")
	fake := &fakeHerdRReceiver{t: t, kind: kind, fixture: fixture}
	previous := herdrRunner
	herdrRunner = fake.run
	t.Cleanup(func() {
		herdrRunner = previous
		if fake.app != nil {
			fake.app.Close()
		}
	})
	return fake
}

// assertRefused: the real receiver refused, registered nothing, wrote nothing.
func (f *fakeHerdRReceiver) assertRefused(t *testing.T) {
	t.Helper()
	if !f.ran {
		t.Fatal("receiver never ran")
	}
	if f.app != nil || f.err == nil {
		t.Fatalf("receiver accepted a %s swap: app=%v err=%v", f.kind, f.app != nil, f.err)
	}
	f.fixture.assertUntouched(t)
}

// assertAccepted: the receiver bound exactly the identified A and acked.
func (f *fakeHerdRReceiver) assertAccepted(t *testing.T, line, col int) {
	t.Helper()
	if f.err != nil || f.app == nil {
		t.Fatalf("receiver refused the normal path: %v", f.err)
	}
	tab := f.app.activeTabPtr()
	if len(f.app.tabs) != 1 || tab.Path != f.fixture.a || tab.Cursor != posAt(line-1, col-1) || tab.Buffer.String() != "AAA\n" {
		t.Fatalf("receiver tab = %q %+v", tab.Path, tab.Cursor)
	}
	if got, _ := tab.BoundFileID(); got.String() != f.args.fileID {
		t.Fatalf("bound file %v, want %s", got, f.args.fileID)
	}
	if got, _ := tab.BoundParentID(); got.String() != f.args.parent {
		t.Fatalf("bound parent %v, want %s", got, f.args.parent)
	}
}

var handoffSwapKinds = []string{"symlink", "hardlink", "parent"}

// TestLinkNonMarkdownRouteHandoff drives OpenFileInHerdRSplit (the
// --herdr-open non-Markdown route) through the fake manager and the real
// receiver: swaps are refused and the sender reports the missing ack; the
// normal path passes the triple and the ack completes.
func TestLinkNonMarkdownRouteHandoff(t *testing.T) {
	for _, kind := range append([]string{"none"}, handoffSwapKinds...) {
		t.Run(kind, func(t *testing.T) {
			fx := newHandoffSwapFixture(t, ".go")
			fake := installFakeHerdR(t, kind, fx)
			err := OpenFileInHerdRSplit(fx.a, 1, 3)
			if fake.args.file != fx.a || fake.args.h == "" {
				t.Fatalf("launch args = %+v", fake.args)
			}
			for _, p := range []string{fake.args.h, fake.args.h + handoff.ClosedSuffix} {
				if _, statErr := os.Lstat(p); !os.IsNotExist(statErr) {
					t.Fatalf("%s survived the sender", p)
				}
			}
			if kind == "none" {
				if err != nil {
					t.Fatalf("normal path: %v", err)
				}
				fake.assertAccepted(t, 1, 3)
				return
			}
			if !errors.Is(err, handoff.ErrNotConfirmed) {
				t.Fatalf("sender error = %v, want missing ack", err)
			}
			fake.assertRefused(t)
		})
	}
}

// TestExplorerRouteHandoff: an explorer click identifies and holds the file,
// launches with the triple, and waits OFF the UI goroutine; a swap is refused
// and the failure is shown, the normal path completes silently.
func TestExplorerRouteHandoff(t *testing.T) {
	for _, kind := range append([]string{"none"}, handoffSwapKinds...) {
		t.Run(kind, func(t *testing.T) {
			fx := newHandoffSwapFixture(t, ".go")
			fake := installFakeHerdR(t, kind, fx)
			a := newTestApp(t, filepath.Dir(fx.dir))
			a.explorer = true
			start := time.Now()
			a.openTreeFile(fx.a)
			if kind != "none" && time.Since(start) > 250*time.Millisecond {
				t.Fatal("explorer click blocked the UI goroutine for the ack wait")
			}
			done := waitExplorerDone(t, a)
			if kind == "none" {
				if done.err != nil || a.confirmOpen {
					t.Fatalf("normal path: %v modal=%v", done.err, a.confirmOpen)
				}
				fake.assertAccepted(t, 1, 1)
				return
			}
			if !errors.Is(done.err, handoff.ErrNotConfirmed) || !a.confirmOpen || !strings.Contains(strings.Join(a.confirmMessageLines, "\n"), "did not confirm") {
				t.Fatalf("explorer error = %v modal=%v %q", done.err, a.confirmOpen, a.confirmMessageLines)
			}
			fake.assertRefused(t)
		})
	}
}

// TestExplorerResolvesTreeSymlinkOnce: a symlink in the tree is resolved once
// by the explorer; the launch names the canonical target, never the link.
func TestExplorerResolvesTreeSymlinkOnce(t *testing.T) {
	fx := newHandoffSwapFixture(t, ".go")
	fake := installFakeHerdR(t, "none", fx)
	link := filepath.Join(fx.dir, "link.go")
	if err := os.Symlink("A.go", link); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, filepath.Dir(fx.dir))
	a.explorer = true
	a.openTreeFile(link)
	if done := waitExplorerDone(t, a); done.err != nil {
		t.Fatal(done.err)
	}
	fake.assertAccepted(t, 1, 1)
}

// TestHandoffInodeReuseRefused is (a): identify A, unlink A, create a new A;
// the receiver given the held IDs refuses, and the hold guarantees the new A
// has a different DEV:INO. The control reports (never fails on) whether this
// filesystem reuses inode numbers without the hold.
func TestHandoffInodeReuseRefused(t *testing.T) {
	isolatedReceiverEnvironment(t)
	fx := newHandoffSwapFixture(t, ".go")
	s, err := handoff.Identify(fx.a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fx.a); err != nil {
		t.Fatal(err)
	}
	isolatedSaveWrite(t, fx.a, "NEW\n")
	if got := isolatedExpectFileID(t, fx.a); got == s.FileID {
		t.Fatalf("held FILE_ID %s was reused", got)
	}
	app, err := NewIsolatedSingleFileAtExpecting(fx.a, 1, 1, s.ParentID, s.FileID, s.Dir)
	if app != nil || err == nil {
		if app != nil {
			app.Close()
		}
		t.Fatal("receiver accepted the recreated A")
	}
	if _, statErr := os.Lstat(filepath.Join(s.Dir, handoff.AckName)); !os.IsNotExist(statErr) {
		t.Fatal("refused receiver acknowledged")
	}
	if err := s.Await(); !errors.Is(err, handoff.ErrNotConfirmed) {
		t.Fatalf("sender = %v", err)
	}
	isolatedSaveAssertBytes(t, fx.a, "NEW\n")

	// Control: the same unlink+create without a hold.
	c := filepath.Join(fx.dir, "C.go")
	isolatedSaveWrite(t, c, "C\n")
	old := isolatedExpectFileID(t, c)
	if err := os.Remove(c); err != nil {
		t.Fatal(err)
	}
	isolatedSaveWrite(t, c, "C2\n")
	t.Logf("control without hold: inode reused=%v (%s -> %s)", isolatedExpectFileID(t, c) == old, old, isolatedExpectFileID(t, c))
}

// TestHandoffAckAfterInvalidateRefused is (d): a receiver whose HANDOFF was
// already renamed (the sender gave up) refuses: no app, nothing written.
func TestHandoffAckAfterInvalidateRefused(t *testing.T) {
	isolatedReceiverEnvironment(t)
	t.Cleanup(handoff.SetTiming(time.Millisecond, time.Millisecond))
	fx := newHandoffSwapFixture(t, ".go")
	s, err := handoff.Identify(fx.a)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Await(); !errors.Is(err, handoff.ErrNotConfirmed) {
		t.Fatalf("sender = %v", err)
	}
	app, err := NewIsolatedSingleFileAtExpecting(fx.a, 1, 1, s.ParentID, s.FileID, s.Dir)
	if app != nil || err == nil || !strings.Contains(err.Error(), "acknowledge") {
		if app != nil {
			app.Close()
		}
		t.Fatalf("receiver after invalidation: app=%v err=%v", app != nil, err)
	}
	if _, statErr := os.Lstat(s.Dir); !os.IsNotExist(statErr) {
		t.Fatal("refused receiver recreated HANDOFF")
	}
	fx.assertUntouched(t)
	isolatedSaveAssertBytes(t, fx.a, "AAA\n")
}

// TestReceiverRequiresNormalizedAbsolutePaths: FILE and HANDOFF are never
// resolved, so unnormalized spellings are refused before anything opens.
func TestReceiverRequiresNormalizedAbsolutePaths(t *testing.T) {
	isolatedReceiverEnvironment(t)
	fx := newHandoffSwapFixture(t, ".go")
	s, err := handoff.Identify(fx.a)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort(nil)
	for _, tc := range [][2]string{
		{fx.dir + "/./A.go", s.Dir}, {fx.dir + "//A.go", s.Dir}, {"docs/A.go", s.Dir},
		{fx.dir + "/../docs/A.go", s.Dir}, {fx.a, s.Dir + "/"}, {fx.a, "rel"},
	} {
		if app, err := NewIsolatedSingleFileAtExpecting(tc[0], 1, 1, s.ParentID, s.FileID, tc[1]); app != nil || err == nil {
			if app != nil {
				app.Close()
			}
			t.Fatalf("accepted %q", tc)
		}
	}
	if _, statErr := os.Lstat(filepath.Join(s.Dir, handoff.AckName)); !os.IsNotExist(statErr) {
		t.Fatal("refused receiver acknowledged")
	}
}

// TestReceiveHandoffImagePreview: image links and explorer clicks keep working
// as read-only previews under the handoff; a swap before the receiver refuses.
func TestReceiveHandoffImagePreview(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pics")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pic, other := filepath.Join(dir, "A.png"), filepath.Join(dir, "B.png")
	if err := os.WriteFile(pic, isolatedExpectPNG(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, isolatedExpectPNG(), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := handoff.Identify(pic)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort(nil)
	tab, err := ReceiveHandoff(pic, s.ParentID, s.FileID, s.Dir)
	if err != nil {
		t.Fatalf("image handoff refused: %v", err)
	}
	defer tab.Close()
	if !tab.IsImage() {
		t.Fatal("image handoff did not open a preview")
	}
	if _, statErr := os.Lstat(filepath.Join(s.Dir, handoff.AckName)); statErr != nil {
		t.Fatal("image handoff did not acknowledge")
	}

	s2, err := handoff.Identify(other)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Abort(nil)
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("A.png", other); err != nil {
		t.Fatal(err)
	}
	if swapped, err := ReceiveHandoff(other, s2.ParentID, s2.FileID, s2.Dir); swapped != nil || err == nil {
		t.Fatal("swapped image accepted")
	}
	if _, statErr := os.Lstat(filepath.Join(s2.Dir, handoff.AckName)); !os.IsNotExist(statErr) {
		t.Fatal("swapped image acknowledged")
	}
}

// TestReceiveHandoffOrder: the ack exists only after both IDs were checked,
// and a matching receive acks exactly once.
func TestReceiveHandoffOrder(t *testing.T) {
	fx := newHandoffSwapFixture(t, ".go")
	s, err := handoff.Identify(fx.a)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort(nil)
	if tab, err := ReceiveHandoff(fx.a, s.ParentID, isolatedExpectFileID(t, fx.b), s.Dir); tab != nil || err == nil {
		t.Fatal("mismatched FILE_ID accepted")
	}
	if _, statErr := os.Lstat(filepath.Join(s.Dir, handoff.AckName)); !os.IsNotExist(statErr) {
		t.Fatal("mismatch acknowledged")
	}
	tab, err := ReceiveHandoff(fx.a, s.ParentID, s.FileID, s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close()
	if _, statErr := os.Lstat(filepath.Join(s.Dir, handoff.AckName)); statErr != nil {
		t.Fatal("matching receive did not acknowledge")
	}
}
