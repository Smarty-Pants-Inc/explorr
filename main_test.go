// =============================================================================
// File: main_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package main

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Smarty-Pants-Inc/explorr/internal/state"
)

// TestResolveArgs_NoArgsRootsCurrentDir keeps the no-arg path simple:
// "." as rootDir, no file to open, action = edit.
func TestResolveArgs_NoArgsRootsCurrentDir(t *testing.T) {
	got := resolveArgs(nil)
	if got.Action != actionEdit {
		t.Fatalf("action: got %q, want edit", got.Action)
	}
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_DirectoryArgUsesAsRoot pins the existing behaviour:
// passing a directory uses it as the editor's root.
func TestResolveArgs_DirectoryArgUsesAsRoot(t *testing.T) {
	dir := t.TempDir()
	got := resolveArgs([]string{dir})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_FileArgRootsParent is the regression test for the
// "explorr main.go" bug: a file argument should root the editor at
// the file's parent and seed an OpenFile so the user's tab is ready.
func TestResolveArgs_FileArgRootsParent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{target})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_BarefilenameRootsCwd covers the common "explorr
// foo.go" form where the path has no directory component. The
// filepath.Dir of "foo.go" is "." — without the empty-string guard
// we'd hand the editor an empty rootDir and filetree.New would fail.
func TestResolveArgs_BarefilenameRootsCwd(t *testing.T) {
	// Use a real bare filename in a temp cwd so the stat path covers
	// the existing-file branch.
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.WriteFile("bare.txt", []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{"bare.txt"})
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "bare.txt" {
		t.Fatalf("OpenFile: got %q, want bare.txt", got.OpenFile)
	}
}

// TestResolveArgs_MissingFileTreatsAsNew mirrors `vim foo.go` on a
// non-existent path: open the editor at the parent dir with the file
// queued for editing — first save creates it.
func TestResolveArgs_MissingFileTreatsAsNew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.go")

	got := resolveArgs([]string{target})
	if got.Err != nil {
		t.Fatalf("missing file should not be an error, got %v", got.Err)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_VersionFlag covers every flavour of --version we
// accept. Failing here would mean a user typing `--version` lands in
// the editor instead of seeing a printed version.
func TestResolveArgs_VersionFlag(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "-V", "version"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionVersion {
			t.Errorf("flag %q: action = %q, want version", flag, got.Action)
		}
	}
}

// TestResolveArgs_HelpFlag is the equivalent for --help. Like version,
// the multi-spelling list keeps the CLI forgiving.
func TestResolveArgs_HelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionHelp {
			t.Errorf("flag %q: action = %q, want help", flag, got.Action)
		}
	}
}

// TestResolveArgs_OpenAt pins the flag and its error case.
func TestResolveArgs_OpenAt(t *testing.T) {
	res := resolveArgs([]string{"--open-at", "src/a.go:12"})
	if res.Action != actionOpenAt || res.OpenFile != "src/a.go:12" {
		t.Fatalf("got %+v", res)
	}
	if got := resolveArgs([]string{"--open-at"}); got.Err == nil {
		t.Error("--open-at with no argument should be an error, not a silent no-op")
	}
}

func TestResolveArgs_HerdROpenParsesLocalFileURL(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "odd file#1.go")
	if err := os.WriteFile(target, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	u := &url.URL{Scheme: "file", Path: target, RawQuery: "line=3&col=2"}

	got := resolveArgs([]string{"--herdr-open", u.String()})
	if got.Err != nil || got.Action != actionHerdROpen || got.OpenFile != target || got.OpenLine != 3 || got.OpenCol != 2 {
		t.Fatalf("resolved to %+v", got)
	}
	u.Host = "LOCALHOST"
	got = resolveArgs([]string{"--herdr-open", u.String()})
	if got.Err != nil || got.Action != actionHerdROpen || got.OpenFile != target || got.OpenLine != 3 || got.OpenCol != 2 {
		t.Fatalf("LOCALHOST resolved to %+v", got)
	}
}

func TestIsMarkdownFile(t *testing.T) {
	for path, want := range map[string]bool{
		"notes.md":       true,
		"NOTES.MARKDOWN": true,
		"notes.mdx":      false,
		"notes.go":       false,
	} {
		if got := isMarkdownFile(path); got != want {
			t.Errorf("isMarkdownFile(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestOpenHerdRFileRoutesMarkdownToReviewr(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Notes.MD")
	if err := os.WriteFile(target, []byte("# Notes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	reviewrLog := filepath.Join(dir, "reviewr.log")
	helper := filepath.Join(dir, reviewMarkdownHelper)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s|%s|%s|%s\\n' \"$1\" \"$HERDR_WORKSPACE_ID\" \"$HERDR_PANE_ID\" \"$HERDR_PLUGIN_CONTEXT_JSON\" > \"$REVIEWR_LOG\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	fakeHerdr := filepath.Join(dir, "herdr")
	if err := os.WriteFile(fakeHerdr, []byte("#!/bin/sh\nprintf split > \"$HERDR_LOG\"\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	t.Setenv("REVIEWR_LOG", reviewrLog)
	t.Setenv("HERDR_LOG", filepath.Join(dir, "herdr.log"))
	t.Setenv("HERDR_BIN_PATH", fakeHerdr)
	t.Setenv("HERDR_WORKSPACE_ID", "wA")
	t.Setenv("HERDR_PANE_ID", "wA:p1")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"wA","focused_pane_id":"wA:p1"}`)

	if err := openHerdRFile(target, 3, 2); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(reviewrLog)
	if err != nil {
		t.Fatal(err)
	}
	want := target + "|wA|wA:p1|{\"workspace_id\":\"wA\",\"focused_pane_id\":\"wA:p1\"}\n"
	if string(got) != want {
		t.Fatalf("Reviewr arguments/context = %q, want %q", got, want)
	}
	if _, err := os.Stat(os.Getenv("HERDR_LOG")); !os.IsNotExist(err) {
		t.Fatalf("Markdown dispatch ran Explorr split command: %v", err)
	}
}

func TestResolveArgs_HerdROpenRejectsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "target.go")
	if err := os.WriteFile(file, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		"https://example.com/target.go",
		"file://remote.example.com" + file,
		(&url.URL{Scheme: "file", Path: dir}).String(),
		(&url.URL{Scheme: "file", Path: filepath.Join(dir, "missing.go")}).String(),
		(&url.URL{Scheme: "file", Path: file, RawQuery: "line=0"}).String(),
	} {
		if got := resolveArgs([]string{"--herdr-open", target}); got.Err == nil {
			t.Errorf("accepted unsafe target %q: %+v", target, got)
		}
	}
	if got := resolveArgs([]string{"--herdr-open"}); got.Err == nil {
		t.Error("--herdr-open without a URL should fail")
	}
}

func TestOpenHerdRFileMarkdownUsesReviewr(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "opened")
	helperDir := t.TempDir()
	helper := filepath.Join(helperDir, "herdr-review-last-markdown")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$EXPLORR_TEST_REVIEWR_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", helperDir)
	t.Setenv("EXPLORR_TEST_REVIEWR_MARKER", marker)

	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(string, int, int) error {
		t.Fatal("Markdown should open in Reviewr when its helper is installed")
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	for _, name := range []string{"notes.md", "NOTES.MARKDOWN"} {
		target := filepath.Join(t.TempDir(), "odd file "+name)
		if err := openHerdRFile(target, 3, 2); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != target {
			t.Errorf("Reviewr path = %q, want decoded path %q", got, target)
		}
	}
}

func TestOpenHerdRFileMarkdownFindsReviewrOffPATH(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "opened")
	home := t.TempDir()
	helperDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(helperDir, "herdr-review-last-markdown")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$EXPLORR_TEST_REVIEWR_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	t.Setenv("EXPLORR_TEST_REVIEWR_MARKER", marker)

	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(string, int, int) error {
		t.Fatal("Reviewr in ~/.local/bin should open before Explorr fallback")
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	target := filepath.Join(t.TempDir(), "notes.md")
	if err := openHerdRFile(target, 3, 2); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != target {
		t.Errorf("Reviewr path = %q, want %q", got, target)
	}
}

func TestOpenHerdRFileMarkdownFallsBackToExplorr(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	target := filepath.Join(t.TempDir(), "notes.md")
	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(path string, line, col int) error {
		if path != target || line != 7 || col != 4 {
			t.Errorf("Explorr open = (%q, %d, %d), want (%q, 7, 4)", path, line, col, target)
		}
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	if err := openHerdRFile(target, 7, 4); err != nil {
		t.Fatal(err)
	}
}

func TestOpenHerdRFileMarkdownFallsBackAfterReviewrFailure(t *testing.T) {
	helperDir := t.TempDir()
	helper := filepath.Join(helperDir, "herdr-review-last-markdown")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", helperDir)
	target := filepath.Join(t.TempDir(), "notes.md")
	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(path string, line, col int) error {
		if path != target || line != 8 || col != 5 {
			t.Errorf("Explorr open = (%q, %d, %d), want (%q, 8, 5)", path, line, col, target)
		}
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	if err := openHerdRFile(target, 8, 5); err != nil {
		t.Fatal(err)
	}
}

func TestOpenHerdRFileNonMarkdownUsesExplorr(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	target := filepath.Join(t.TempDir(), "source.go")
	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(path string, line, col int) error {
		if path != target || line != 12 || col != 9 {
			t.Errorf("Explorr open = (%q, %d, %d), want (%q, 12, 9)", path, line, col, target)
		}
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	if err := openHerdRFile(target, 12, 9); err != nil {
		t.Fatal(err)
	}
}

func TestResolveArgs_SingleFileAtCarriesPosition(t *testing.T) {
	file := filepath.Join(t.TempDir(), "target.go")
	if err := os.WriteFile(file, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	got := resolveArgs([]string{"--single-file-at", file, "12", "4"})
	if got.Err != nil || got.Action != actionEdit || got.OpenFile != file || got.OpenLine != 12 || got.OpenCol != 4 {
		t.Fatalf("resolved to %+v", got)
	}
}

func TestResolveArgs_ExplorerMode(t *testing.T) {
	dir := t.TempDir()
	got := resolveArgs([]string{"--explorer", dir})
	if got.Err != nil || got.Action != actionExplorer || got.RootDir != dir || got.OpenFile != "" {
		t.Fatalf("explicit explorer directory resolved to %+v", got)
	}

	got = resolveArgs([]string{"--explorer"})
	if got.Err != nil || got.Action != actionExplorer || got.RootDir != "." {
		t.Fatalf("default explorer directory resolved to %+v", got)
	}

	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := resolveArgs([]string{"--explorer", file}); got.Err == nil {
		t.Fatal("explorer accepted a file root")
	}
	if got := resolveArgs([]string{"--explorer", dir, "extra"}); got.Err == nil {
		t.Fatal("explorer accepted more than one directory")
	}
}

// TestResolveArgs_Debug pins the flag the Debug panel drives the editor with.
//
// Every key in that panel becomes one of these invocations, so the parse has to
// be exact in three places: the verb is accepted, an unknown verb is REFUSED
// here rather than becoming a key that silently does nothing, and
// toggle-breakpoint refuses without a location because it has nothing to toggle.
func TestResolveArgs_Debug(t *testing.T) {
	for _, action := range state.DebugActions() {
		args := []string{"--debug", action}
		if action == state.DebugActionToggleBreakpoint {
			args = append(args, "src/a.go:12")
		}
		got := resolveArgs(args)
		if got.Err != nil {
			t.Errorf("--debug %s: unexpected error %v", action, got.Err)
			continue
		}
		if got.Action != actionDebug || got.DebugAction != action {
			t.Errorf("--debug %s resolved to %+v", action, got)
		}
	}

	if got := resolveArgs([]string{"--debug"}); got.Err == nil {
		t.Error("--debug with no action should be an error, not a silent no-op")
	}
	if got := resolveArgs([]string{"--debug", "contnue"}); got.Err == nil {
		t.Error("a misspelled action should be reported, not written for the editor to ignore")
	}
	if got := resolveArgs([]string{"--debug", "toggle-breakpoint"}); got.Err == nil {
		t.Error("toggle-breakpoint with no location should be an error")
	}
}

// TestResolveArgs_DebugCarriesTheLocation pins that the optional location
// survives the parse untouched. It is split by state.SplitLocation in main —
// the SAME parser --open-at uses — so this only has to prove the string is
// carried, not re-implement the split.
func TestResolveArgs_DebugCarriesTheLocation(t *testing.T) {
	got := resolveArgs([]string{"--debug", state.DebugActionToggleBreakpoint, "/proj/main.go:42"})
	if got.OpenFile != "/proj/main.go:42" {
		t.Fatalf("location = %q, want it carried through verbatim", got.OpenFile)
	}
	// A verb that takes no location must not acquire one by accident.
	if plain := resolveArgs([]string{"--debug", state.DebugActionContinue}); plain.OpenFile != "" {
		t.Fatalf("continue picked up a location %q", plain.OpenFile)
	}
}

// TestHelpNamesEveryDebugAction guards the gap between a CLI that accepts a
// verb and a user who can find out it exists. The help text is the only place
// the actions are listed for a human, and a verb added to state.DebugActions()
// without a mention here is a feature nobody can discover — the same
// "implementing the request is the easy half" trap CLAUDE.md records for the
// LSP features that shipped with no call site.
//
// It reads the ACTUAL printed output rather than the source literal, so a help
// block that stops printing would fail too.
func TestHelpNamesEveryDebugAction(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	printHelp()
	os.Stdout = saved
	w.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	help := string(out)
	for _, action := range state.DebugActions() {
		if !strings.Contains(help, action) {
			t.Errorf("--help does not mention the %q action", action)
		}
	}
}

func TestREADMEReflectsPublishedRelease(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(readme))
	for _, stale := range []string{
		"no published release yet",
		"until then, build from source",
		"installer will likewise be available",
	} {
		if strings.Contains(lower, stale) {
			t.Errorf("released README retains pre-release wording %q", stale)
		}
	}
	for _, required := range []string{
		"https://github.com/Smarty-Pants-Inc/explorr/releases/latest",
		"brew install Smarty-Pants-Inc/explorr/explorr",
		"curl -fsSL https://raw.githubusercontent.com/Smarty-Pants-Inc/explorr/main/install.sh | sh",
		"herdr plugin install Smarty-Pants-Inc/explorr/herdr",
		"bundled lifecycle requires `explorr` and `jq` on",
	} {
		if !strings.Contains(string(readme), required) {
			t.Errorf("released README is missing %q", required)
		}
	}
}
