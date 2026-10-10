//go:build linux || darwin

// =============================================================================
// File: main_handoff_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Smarty-Pants-Inc/explorr/internal/app"
	"github.com/Smarty-Pants-Inc/explorr/internal/handoff"
)

// mainLstatID is path's own DEV:INO per the shared contract.
func mainLstatID(t *testing.T, path string, follow bool) (string, bool) {
	t.Helper()
	var st syscall.Stat_t
	var err error
	if follow {
		err = syscall.Stat(path, &st)
	} else {
		err = syscall.Lstat(path, &st)
	}
	if err != nil {
		return "", false
	}
	return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(uint64(st.Ino), 10), st.Mode&syscall.S_IFMT == syscall.S_IFREG
}

// mainSwapFixture is docs/A<ext> (clicked) and the victim docs/B<ext>.
type mainSwapFixture struct{ dir, a, b string }

func newMainSwapFixture(t *testing.T, ext string) mainSwapFixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "docs")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := mainSwapFixture{dir: dir, a: filepath.Join(dir, "A"+ext), b: filepath.Join(dir, "B"+ext)}
	for p, s := range map[string]string{f.a: "AAA\n", f.b: "BBB\n"} {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// swap replaces A between the sender's identify and the receiver.
func (f mainSwapFixture) swap(t *testing.T, kind string) {
	t.Helper()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	switch kind {
	case "symlink":
		must(os.Remove(f.a))
		must(os.Symlink(filepath.Base(f.b), f.a))
	case "hardlink":
		must(os.Remove(f.a))
		must(os.Link(f.b, f.a))
	case "parent":
		old := f.dir + ".old"
		must(os.Rename(f.dir, old))
		must(os.Mkdir(f.dir, 0o755))
		must(os.Link(filepath.Join(old, filepath.Base(f.a)), f.a))
		must(os.Link(filepath.Join(old, filepath.Base(f.b)), f.b))
	}
}

func (f mainSwapFixture) assertUntouched(t *testing.T) {
	t.Helper()
	if data, err := os.ReadFile(f.b); err != nil || string(data) != "BBB\n" {
		t.Fatalf("victim B = %q, %v", data, err)
	}
}

// emulatedReviewrReceiver applies the contract's helper+receiver checks with
// NO resolution of FILE: lstat regular with FILE_ID, stat(dirname) == PARENT_ID,
// then the shared ack step. It returns whether it "opened" the file.
func emulatedReviewrReceiver(t *testing.T, argv []string) bool {
	t.Helper()
	file, parent, fileID, dir, nonce := argv[0], argv[3], argv[4], argv[5], argv[6]
	if handoff.CheckPath(file) != nil {
		return false
	}
	if got, regular := mainLstatID(t, file, false); !regular || got != fileID {
		return false
	}
	if got, _ := mainLstatID(t, filepath.Dir(file), true); got != parent {
		return false
	}
	return handoff.Acknowledge(dir, nonce, fileID) == nil
}

// installReviewHelper puts a helper on PATH and replaces its execution with a
// recording Go fake that swaps and then emulates the receiver.
func installReviewHelper(t *testing.T, onRun func(argv []string)) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, reviewMarkdownHelper), []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HERDR_PLUGIN_ROOT", "")
	previous := runReviewHelper
	runReviewHelper = func(helper string, args ...string) ([]byte, error) {
		if filepath.Base(helper) != reviewMarkdownHelper {
			t.Fatalf("helper = %q", helper)
		}
		onRun(args)
		return nil, nil
	}
	t.Cleanup(func() { runReviewHelper = previous })
	previousSplit := openFileInHerdRSplit
	openFileInHerdRSplit = func(string, int, int) error {
		t.Fatal("Markdown must not open Explorr")
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previousSplit })
	t.Cleanup(handoff.SetTiming(300*time.Millisecond, 5*time.Millisecond))
}

// TestMarkdownRouteHandoff: the --herdr-open Markdown route calls the helper
// with exactly FILE LINE COL PARENT_ID FILE_ID HANDOFF NONCE and waits for the ack;
// symlink, hard-link and parent swaps after identify are refused, nothing is
// opened, the victim is untouched and the sender reports the missing ack.
func TestMarkdownRouteHandoff(t *testing.T) {
	for _, kind := range []string{"none", "symlink", "hardlink", "parent"} {
		t.Run(kind, func(t *testing.T) {
			fx := newMainSwapFixture(t, ".md")
			wantParent, _ := mainLstatID(t, fx.dir, false)
			wantFile, _ := mainLstatID(t, fx.a, false)
			var argv []string
			opened := false
			installReviewHelper(t, func(args []string) {
				argv = args
				fx.swap(t, kind)
				opened = emulatedReviewrReceiver(t, args)
			})
			err := openHerdRFile(fx.a, 7, 3, false)
			if len(argv) != 7 || handoff.CheckNonce(argv[6]) != nil || argv[0] != fx.a || argv[1] != "7" || argv[2] != "3" || argv[3] != wantParent || argv[4] != wantFile {
				t.Fatalf("helper argv = %q", argv)
			}
			if !strings.HasPrefix(filepath.Base(argv[5]), handoff.DirPrefix) {
				t.Fatalf("HANDOFF = %q", argv[5])
			}
			for _, p := range []string{argv[5], argv[5] + handoff.ClosedSuffix} {
				if _, statErr := os.Lstat(p); !os.IsNotExist(statErr) {
					t.Fatalf("%s survived the sender", p)
				}
			}
			if kind == "none" {
				if err != nil || !opened {
					t.Fatalf("normal path: opened=%v err=%v", opened, err)
				}
				return
			}
			if opened || !errors.Is(err, handoff.ErrNotConfirmed) {
				t.Fatalf("%s swap: opened=%v err=%v", kind, opened, err)
			}
			fx.assertUntouched(t)
		})
	}
}

// TestMarkdownRouteRealHelperProcess runs an actual helper executable that
// acks HANDOFF ($6), proving the argv reaches a real process and the sender
// waits for it before returning.
func TestMarkdownRouteRealHelperProcess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" > \"$MARKER\"\n( sleep 0.1; printf '%s %s\\n' \"$7\" \"$5\" > \"$6/ack\" ) &\n"
	if err := os.WriteFile(filepath.Join(dir, reviewMarkdownHelper), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	t.Setenv("HERDR_PLUGIN_ROOT", "")
	t.Setenv("MARKER", marker)
	fx := newMainSwapFixture(t, ".markdown")
	if err := openHerdRFile(fx.a, 2, 1, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if err != nil || len(lines) != 8 || lines[0] != "7" || lines[1] != fx.a || lines[2] != "2" || lines[3] != "1" {
		t.Fatalf("helper argv = %q, %v", lines, err)
	}
}

// TestNonMarkdownLinkRouteHandoff drives the real non-Markdown sender
// (app.OpenFileInHerdRSplitWith) from openHerdRFile with a recording fake
// manager whose `pane run` swaps and runs the real receiver bind/check/ack.
func TestNonMarkdownLinkRouteHandoff(t *testing.T) {
	for _, kind := range []string{"none", "symlink", "hardlink", "parent"} {
		t.Run(kind, func(t *testing.T) {
			t.Cleanup(handoff.SetTiming(300*time.Millisecond, 5*time.Millisecond))
			t.Setenv("HERDR_PANE_ID", "wA:p7")
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			fx := newMainSwapFixture(t, ".go")
			var command string
			received := false
			runner := func(bin string, args ...string) ([]byte, error) {
				if len(args) >= 2 && args[1] == "split" {
					return []byte(`{"result":{"pane":{"pane_id":"wA:p8"}}}`), nil
				}
				if len(args) == 5 && args[1] == "run" && args[2] == "--allow-cross-pane" {
					command = args[4]
					fx.swap(t, kind)
					w := strings.Fields(command)
					if len(w) != 14 || w[2] != "--single-file-at" || w[6] != "--expect-parent" || w[8] != "--expect-file" || w[10] != "--handoff" || w[12] != "--handoff-nonce" {
						t.Fatalf("command = %q", command)
					}
					unq := func(s string) string { return strings.Trim(s, "'") }
					tab, err := app.ReceiveHandoff(unq(w[3]), unq(w[7]), unq(w[9]), unq(w[11]), unq(w[13]))
					if err == nil {
						received = true
						if tab.Buffer.String() != "AAA\n" {
							t.Errorf("receiver loaded %q", tab.Buffer.String())
						}
						_ = tab.Close()
					}
				}
				return []byte(`{"result":{}}`), nil
			}
			previous := openFileInHerdRSplit
			openFileInHerdRSplit = func(path string, line, col int) error {
				return app.OpenFileInHerdRSplitWith(runner, path, line, col)
			}
			t.Cleanup(func() { openFileInHerdRSplit = previous })
			err := openHerdRFile(fx.a, 4, 2, false)
			if !strings.Contains(command, " --single-file-at "+"'"+fx.a+"'"+" 4 2 --expect-parent ") {
				t.Fatalf("command = %q", command)
			}
			if kind == "none" {
				if err != nil || !received {
					t.Fatalf("normal path: received=%v err=%v", received, err)
				}
				return
			}
			if received || !errors.Is(err, handoff.ErrNotConfirmed) {
				t.Fatalf("%s swap: received=%v err=%v", kind, received, err)
			}
			fx.assertUntouched(t)
		})
	}
}

// TestResolveArgsSingleFileAtRequiresTriple is (e): the bare form and every
// incomplete, duplicated, extra, malformed or misplaced triple is refused.
func TestResolveArgsSingleFileAtRequiresTriple(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "target.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := filepath.Join(dir, "handoff")
	p, f := "2049:18446744073709551615", "18446744073709551615:7"
	n := "00112233445566778899aabbccddeeff"
	ok := [][]string{
		{"--single-file-at", file, "3", "5", "--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n},
		{"--single-file-at", file, "3", "5", "--handoff", h, "--expect-file", f, "--expect-parent", p, "--handoff-nonce", n},
		{"--single-file-at", file, "3", "5", "--expect-file", f, "--handoff", h, "--expect-parent", p, "--handoff-nonce", n},
	}
	for _, args := range ok {
		got := resolveArgs(args)
		if got.Err != nil || !got.Isolated || got.OpenFile != file || got.OpenLine != 3 || got.OpenCol != 5 ||
			got.ExpectParent != p || got.ExpectFile != f || got.Handoff != h || got.HandoffNonce != n {
			t.Fatalf("%q resolved to %+v", args, got)
		}
	}
	base := []string{"--single-file-at", file, "3", "5"}
	raw := func(extra ...string) []string { return append(append([]string(nil), base...), extra...) }
	with := func(extra ...string) []string { return append(raw(extra...), "--handoff-nonce", n) }
	bad := [][]string{
		base,
		// The nonce is required, exactly once, as 32 lowercase hex digits.
		raw("--expect-parent", p, "--expect-file", f, "--handoff", h),
		with("--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n),
		raw("--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", ""),
		raw("--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", "00112233445566778899AABBCCDDEEFF"),
		raw("--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n[:30]),
		raw("--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n+"00"),
		raw("--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", "zz"+n[2:]),
		with("--expect-parent", p, "--expect-file", f),
		with("--expect-parent", p, "--handoff", h),
		with("--expect-file", f, "--handoff", h),
		with("--expect-parent", p, "--expect-file", f, "--handoff"),
		with("--expect-parent", p, "--expect-parent", p, "--handoff", h),
		with("--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff", h),
		with("--expect-parent", p, "--expect-file", f, "--handoff", h, "extra"),
		with("--expect-parent", "1:x", "--expect-file", f, "--handoff", h),
		with("--expect-parent", p, "--expect-file", "12", "--handoff", h),
		with("--expect-parent", p, "--expect-file", f, "--handoff", "relative"),
		with("--expect-parent", p, "--expect-file", f, "--handoff", h+"/"),
		with("--expect-parent", p, "--expect-file", f, "--handoff", dir+"/./handoff"),
		with("--expect-parent", p, "--expect-file", f, "--handoff", ""),
		with("--expect", p, "--expect-file", f, "--handoff", h),
		{"--single-file-at", dir + "/./target.go", "3", "5", "--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n},
		{"--single-file-at", "target.go", "3", "5", "--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n},
		{"--single-file-at", file + "/", "3", "5", "--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n},
		{"--single-file-at", file, "0", "5", "--expect-parent", p, "--expect-file", f, "--handoff", h, "--handoff-nonce", n},
		{"--single-file-at", file, "--expect-parent", p, "--expect-file", f, "--handoff", h, "3", "5"},
		{"--handoff", h},
		{file, "--handoff", h},
		{"--open-at", file, "--handoff", h},
		{"--explorer", dir, "--handoff", h},
		{"--herdr-open", "file://" + file, "--handoff", h},
	}
	for _, args := range bad {
		if got := resolveArgs(args); got.Err == nil {
			t.Errorf("%q accepted: %+v", args, got)
		}
	}
}

// TestResolveArgsSingleFileAtDoesNotResolveFile: a symlink FILE is not
// resolved or stat'ed by the parser; the receiver's no-follow bind refuses it.
func TestResolveArgsSingleFileAtDoesNotResolveFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.go")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got := resolveArgs([]string{"--single-file-at", link, "1", "1", "--expect-parent", "1:2", "--expect-file", "3:4", "--handoff", filepath.Join(dir, "h"), "--handoff-nonce", "00112233445566778899aabbccddeeff"})
	if got.Err != nil || got.OpenFile != link {
		t.Fatalf("parser resolved or refused FILE: %+v", got)
	}
}
