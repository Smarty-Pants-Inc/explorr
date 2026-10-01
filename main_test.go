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
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	u := &url.URL{Scheme: "file", Path: target, RawQuery: "line=3&col=2"}

	got := resolveArgs([]string{"--herdr-open", u.String()})
	if got.Err != nil || got.Action != actionHerdROpen || got.OpenFile != canonical || got.OpenLine != 3 || got.OpenCol != 2 || got.ReviewFile {
		t.Fatalf("resolved to %+v", got)
	}
	u.Host = "LOCALHOST"
	got = resolveArgs([]string{"--herdr-open", u.String()})
	if got.Err != nil || got.Action != actionHerdROpen || got.OpenFile != canonical || got.OpenLine != 3 || got.OpenCol != 2 || got.ReviewFile {
		t.Fatalf("LOCALHOST resolved to %+v", got)
	}
}

// TestLocalFileHosts pins the exact local aliases without relying on test DNS.
func TestLocalFileHosts(t *testing.T) {
	hosts := localFileHosts("DevBox.internal", "DevBox.example.com")
	for _, host := range []string{"", "localhost", "devbox.internal", "devbox", "devbox.example.com"} {
		if !hosts[host] {
			t.Errorf("missing local host %q", host)
		}
	}
	for _, host := range []string{"remote.example.com", "devbox:80", "user@devbox", "127.0.0.1"} {
		if hosts[host] {
			t.Errorf("unexpected local host %q", host)
		}
	}
}

// TestParseLocalFileURLHappyPaths covers OSC8 fragments, query positions,
// single percent decoding, host aliases and canonical symlink destinations.
func TestParseLocalFileURLHappyPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "odd file#%0a%2F-é;'$.go")
	if err := os.WriteFile(target, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "safe-link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	hosts := localFileHosts("devbox.internal", "devbox.example.com")
	for _, host := range []string{"", "LOCALHOST", "DEVBOX", "devbox.internal", "DEVBOX.EXAMPLE.COM"} {
		for _, clicked := range []string{target, link} {
			for _, position := range []struct {
				query, fragment string
				line, col       int
			}{
				{"", "", 1, 1},
				{"", "42", 42, 1},
				{"line=12&col=3", "", 12, 3},
				{"col=3", "42", 42, 3},
				{"line=12", "", 12, 1},
			} {
				u := (&url.URL{Scheme: "file", Host: host, Path: clicked, RawQuery: position.query, Fragment: position.fragment}).String()
				path, line, col, review, err := parseLocalFileURLForHosts(u, hosts)
				if err != nil || path != canonical || line != position.line || col != position.col || review {
					t.Errorf("%q: got (%q, %d, %d, %v), want (%q, %d, %d)", u, path, line, col, err, canonical, position.line, position.col)
				}
			}
		}
	}
	// The public CLI must carry the canonical path onward, not the clicked name.
	got := resolveArgs([]string{"--herdr-open", (&url.URL{Scheme: "file", Path: link, Fragment: "42"}).String()})
	if got.Err != nil || got.OpenFile != canonical || got.OpenLine != 42 || got.OpenCol != 1 {
		t.Fatalf("canonical OSC8 CLI result: %+v", got)
	}
	if hostname, err := os.Hostname(); err == nil {
		got = resolveArgs([]string{"--herdr-open", (&url.URL{Scheme: "file", Host: hostname, Path: target, Fragment: "42"}).String()})
		if got.Err != nil || got.OpenFile != canonical || got.OpenLine != 42 {
			t.Fatalf("hostname OSC8 CLI result: %+v", got)
		}
	}
}

// TestParseLocalFileURLRejectsUnsafeVariants checks full authorities and every
// ambiguous or unsupported position syntax rather than silently dropping it.
func TestParseLocalFileURLRejectsUnsafeVariants(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.go")
	if err := os.WriteFile(target, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := (&url.URL{Scheme: "file", Path: target}).String()
	hosts := localFileHosts("devbox.internal", "devbox.example.com")
	urls := []string{
		"https://localhost" + target,
		"file:relative.go", "file://localhost/", "file://localhost", "file:relative",
		"file://remote.example.com" + target,
		"file://user@localhost" + target,
		"file://user:password@devbox" + target,
		"file://localhost:80" + target,
		"file://devbox:", "file://[::1]" + target,
		base + "%ZZ", base + "%FF", base + "\n",
	}
	for _, suffix := range []string{
		"?", "?&", "?line=1&", "?&line=1", "?line=1&&col=2",
		"?unknown=1", "?line=1&unknown=1", "?Line=1",
		"?review", "?review=", "?review=0", "?review=true", "?review=01",
		"?review=1&review=1", "?review=1&review=0",
		"?line", "?line=", "?col=", "?line=0", "?col=0",
		"?line=-1", "?line=%2B1", "?line=1.5", "?col=one",
		"?line=١", "?line=1000000000", "?line=99999999999999999999",
		"?line=1&line=1", "?line=1&line=2", "?col=1&col=1",
		"?line=1;col=2", "?line=%ZZ", "?line=1&%6cine=2",
		"#", "#0", "#-1", "#+1", "#1.5", "#١", "#%34%32", "#1000000000",
		"#42#43", "?line=42#42", "?line=1#42", "?line=1&col=2#42",
	} {
		urls = append(urls, base+suffix)
	}
	for _, raw := range urls {
		if path, line, col, _, err := parseLocalFileURLForHosts(raw, hosts); err == nil {
			t.Errorf("accepted %q as (%q, %d, %d)", raw, path, line, col)
		}
	}
}

// TestParseLocalFileURLRejectsControlNames checks both the clicked path and
// resolved destination, including a harmless-looking symlink to an unsafe name.
func TestParseLocalFileURLRejectsControlNames(t *testing.T) {
	dir := t.TempDir()
	hosts := localFileHosts("devbox", "devbox.example.com")
	for r := rune(0); r <= 0x9f; r++ {
		if r >= 0x20 && r < 0x7f {
			continue
		}
		clicked := filepath.Join(dir, "unsafe-"+string(r)+".go")
		raw := (&url.URL{Scheme: "file", Path: clicked}).String()
		if _, _, _, _, err := parseLocalFileURLForHosts(raw, hosts); err == nil || !strings.Contains(err.Error(), "control") {
			t.Errorf("control U+%04X should be rejected before filesystem access: %v", r, err)
		}
		// NUL cannot occur in filesystem names; all other controls can.
		if r == 0 {
			continue
		}
		if err := os.WriteFile(clicked, []byte("unsafe"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link-"+strings.TrimPrefix(url.QueryEscape(string(r)), "%"))
		if err := os.Symlink(clicked, link); err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := parseLocalFileURLForHosts((&url.URL{Scheme: "file", Path: link}).String(), hosts); err == nil {
			t.Errorf("accepted symlink destination with control U+%04X", r)
		}
	}
}

// TestParseLocalFileURLRejectsNonRegularTargets pins missing, dangling, looping,
// directory and device paths; os.Stat alone used to accept non-directory devices.
func TestParseLocalFileURLRejectsNonRegularTargets(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	dangling := filepath.Join(dir, "dangling")
	loop := filepath.Join(dir, "loop")
	for link, target := range map[string]string{dangling: missing, loop: loop} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	targets := []string{dir, missing, dangling, loop}
	if info, err := os.Stat("/dev/null"); err == nil && !info.Mode().IsRegular() {
		targets = append(targets, "/dev/null")
	}
	for _, target := range targets {
		if _, _, _, _, err := parseLocalFileURL((&url.URL{Scheme: "file", Path: target}).String()); err == nil {
			t.Errorf("accepted non-regular target %q", target)
		}
	}
}

func TestOpenHerdRFileMarkdownUsesReviewr(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "opened")
	helperDir := t.TempDir()
	helper := filepath.Join(helperDir, "herdr-review-last-markdown")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s|%s|%s' \"$1\" \"$2\" \"$3\" > \"$EXPLORR_TEST_REVIEWR_MARKER\"\n"), 0o755); err != nil {
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
		if err := openHerdRFile(target, 3, 2, false); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		if want := target + "|3|2"; string(got) != want {
			t.Errorf("Reviewr target = %q, want %q", got, want)
		}
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

func TestOpenHerdRFileMarkdownFindsReviewrOffPATH(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "opened")
	home := t.TempDir()
	helperDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(helperDir, "herdr-review-last-markdown")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s|%s|%s' \"$1\" \"$2\" \"$3\" > \"$EXPLORR_TEST_REVIEWR_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	t.Setenv("EXPLORR_TEST_REVIEWR_MARKER", marker)

	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(string, int, int) error {
		t.Fatal("Reviewr in ~/.local/bin must open without an Explorr fallback")
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	target := filepath.Join(t.TempDir(), "notes.md")
	if err := openHerdRFile(target, 3, 2, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if want := target + "|3|2"; string(got) != want {
		t.Errorf("Reviewr target = %q, want %q", got, want)
	}
}

// TestOpenHerdRFileReviewrMissingFailsClosed protects both default Markdown
// routing and explicit non-Markdown review requests from silently using Explorr.
func TestOpenHerdRFileReviewrMissingFailsClosed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(string, int, int) error {
		t.Fatal("missing Reviewr helper must not fall back to Explorr")
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	for _, name := range []string{"notes.md", "NOTES.MARKDOWN", "notes.ts"} {
		err := openHerdRFile(filepath.Join(t.TempDir(), name), 7, 4, name == "notes.ts")
		if err == nil || !strings.Contains(err.Error(), "deployment-provided helper") || !strings.Contains(err.Error(), reviewMarkdownHelper) {
			t.Errorf("%s: expected missing-helper diagnostic, got %v", name, err)
		}
	}
}

// TestOpenHerdRFileReviewrFailureFailsClosed also checks that helper stderr is
// surfaced, not hidden behind a fallback editor or a generic exit-status error.
func TestOpenHerdRFileReviewrFailureFailsClosed(t *testing.T) {
	helperDir := t.TempDir()
	helper := filepath.Join(helperDir, reviewMarkdownHelper)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'exact file unsupported' >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", helperDir)
	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(string, int, int) error {
		t.Fatal("failing Reviewr helper must not fall back to Explorr")
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	for _, name := range []string{"notes.md", "notes.json"} {
		err := openHerdRFile(filepath.Join(t.TempDir(), name), 8, 5, name == "notes.json")
		if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "exact file unsupported") {
			t.Errorf("%s: expected helper status and diagnostic, got %v", name, err)
		}
	}
}

// TestResolveArgsReviewOptInRoutesExactFile exercises parser → CLI result →
// routing together, with a canonical explicit filename and preserved position.
func TestResolveArgsReviewOptInRoutesExactFile(t *testing.T) {
	helperDir := t.TempDir()
	marker := filepath.Join(helperDir, "opened")
	helper := filepath.Join(helperDir, reviewMarkdownHelper)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s|%s|%s|%s' \"$#\" \"$1\" \"$2\" \"$3\" > \"$EXPLORR_TEST_REVIEWR_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", helperDir)
	t.Setenv("EXPLORR_TEST_REVIEWR_MARKER", marker)
	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(string, int, int) error {
		t.Fatal("review=1 must never open Explorr")
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	for _, name := range []string{"explicit file;'$.ts", "config.json", "notes.md"} {
		dir := t.TempDir()
		target := filepath.Join(dir, name)
		if err := os.WriteFile(target, []byte("review this"), 0o644); err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "clicked-link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		for _, u := range []*url.URL{
			{Scheme: "file", Path: link, RawQuery: "review=1&line=12&col=9"},
			{Scheme: "file", Path: link, RawQuery: "review=1&col=9", Fragment: "12"},
		} {
			res := resolveArgs([]string{"--herdr-open", u.String()})
			if res.Err != nil || !res.ReviewFile || res.Action != actionHerdROpen || res.OpenFile != canonical {
				t.Fatalf("review opt-in result: %+v", res)
			}
			if err := openHerdRFile(res.OpenFile, res.OpenLine, res.OpenCol, res.ReviewFile); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if want := "3|" + canonical + "|12|9"; string(got) != want {
				t.Errorf("helper argv = %q, want %q", got, want)
			}
		}
	}
}

func TestOpenHerdRFileNonMarkdownBypassesReviewr(t *testing.T) {
	helperDir := t.TempDir()
	marker := filepath.Join(helperDir, "reviewr-invoked")
	helper := filepath.Join(helperDir, reviewMarkdownHelper)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf invoked > \"$EXPLORR_TEST_REVIEWR_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", helperDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EXPLORR_TEST_REVIEWR_MARKER", marker)

	target := filepath.Join(t.TempDir(), "notes.mdx")
	splitCalled := false
	previous := openFileInHerdRSplit
	openFileInHerdRSplit = func(path string, line, col int) error {
		splitCalled = true
		if path != target || line != 12 || col != 9 {
			t.Errorf("Explorr open = (%q, %d, %d), want (%q, 12, 9)", path, line, col, target)
		}
		return nil
	}
	t.Cleanup(func() { openFileInHerdRSplit = previous })

	if err := openHerdRFile(target, 12, 9, false); err != nil {
		t.Fatal(err)
	}
	if !splitCalled {
		t.Fatal("non-Markdown file did not open in Explorr")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("non-Markdown file invoked Reviewr: %v", err)
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
