//go:build linux || darwin

// =============================================================================
// File: internal/app/isolated_mutators_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Smarty-Pants-Inc/explorr/internal/customactions"
	"github.com/Smarty-Pants-Inc/explorr/internal/filetree"
	"github.com/Smarty-Pants-Inc/explorr/internal/lsp"
)

// isolatedMutatorRefused asserts the user-visible refusal for one action.
func isolatedMutatorRefused(t *testing.T, a *App, action string) {
	t.Helper()
	want := isolatedRefusalPrefix + action
	if a.statusMsg != want {
		t.Errorf("status = %q, want %q", a.statusMsg, want)
	}
}

// isolatedMutatorApp opens the real isolated constructor on original/file.txt,
// with a same-basename victim in a sibling directory.
func isolatedMutatorApp(t *testing.T) (*App, string, string) {
	t.Helper()
	isolatedReceiverEnvironment(t)
	path, victim := isolatedSaveFiles(t)
	a, err := NewIsolatedSingleFileAt(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, path, victim
}

// isolatedMutatorEdit replaces the first three characters of line one.
func isolatedMutatorEdit(path, text string) map[string][]lsp.TextEdit {
	return map[string][]lsp.TextEdit{path: {{
		Range:   lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 3}},
		NewText: text,
	}}}
}

// TestIsolatedRenameSymbolRefused covers both the request and a result landing
// after A was swapped to a symlink to B: B must stay intact.
func TestIsolatedRenameSymbolRefused(t *testing.T) {
	a, path, victim := isolatedMutatorApp(t)
	isolatedPublisherMenu(t, a, "Rename symbol")
	if a.promptOpen {
		t.Fatal("rename prompt opened in isolated pane")
	}
	isolatedMutatorRefused(t, a, "Rename symbol")
	a.renameSubmit("neu")
	isolatedMutatorRefused(t, a, "Rename symbol")

	held, _ := isolatedSaveReplace(t, path, victim, "symlink")
	a.statusMsg = ""
	a.handleRename(&renameEvent{edits: isolatedMutatorEdit(path, "ZZZ"), newName: "ZZZ", when: time.Now()})
	isolatedMutatorRefused(t, a, "Rename symbol")
	isolatedSaveAssertBytes(t, victim, "BBB\n")
	isolatedSaveAssertBytes(t, held, "AAA\n")
}

// TestIsolatedFixAtCursorRefused covers the request, the result list, and the
// final apply point of a fix list built before the swap.
func TestIsolatedFixAtCursorRefused(t *testing.T) {
	a, path, victim := isolatedMutatorApp(t)
	isolatedPublisherMenu(t, a, "Fix at cursor")
	isolatedMutatorRefused(t, a, "Fix at cursor")

	actions := []lsp.CodeAction{{Title: "Fix it", Edits: isolatedMutatorEdit(path, "ZZZ")}}
	a.handleCodeActions(&codeActionsEvent{actions: actions, when: time.Now()})
	if a.paletteOpen {
		// Pre-fix behaviour: the list opened. Exercise the apply anyway.
		held, _ := isolatedSaveReplace(t, path, victim, "symlink")
		a.paletteSelected = 0
		a.runSelectedPaletteCommand()
		isolatedSaveAssertBytes(t, victim, "BBB\n")
		isolatedSaveAssertBytes(t, held, "AAA\n")
		t.Fatal("fix list opened in isolated pane")
	}
	isolatedMutatorRefused(t, a, "Fix at cursor")

	// Final apply point: the shared writer refuses even when called directly.
	held, _ := isolatedSaveReplace(t, path, victim, "symlink")
	a.statusMsg = ""
	a.applyEditsAndReload(isolatedMutatorEdit(path, "ZZZ"), "Fix at cursor", func(files, count int, err error) string { return "applied" })
	isolatedMutatorRefused(t, a, "Fix at cursor")
	isolatedSaveAssertBytes(t, victim, "BBB\n")
	isolatedSaveAssertBytes(t, held, "AAA\n")
}

// TestIsolatedDeletePendingConfirmation replaces the parent while a Delete
// confirmation is pending; confirming must leave the replacement untouched.
func TestIsolatedDeletePendingConfirmation(t *testing.T) {
	for _, route := range []string{"menu", "pending"} {
		t.Run(route, func(t *testing.T) {
			a, path, victim := isolatedMutatorApp(t)
			if route == "menu" {
				isolatedPublisherMenu(t, a, "Delete file")
			} else {
				// A confirmation captured before isolation could refuse it
				// (the exact callback menuDelete arms): guard the final point.
				target := path
				a.openConfirm("Delete file", "Permanently delete?", func(app *App) { app.doDeletePath(target) })
			}
			pending := a.confirmOpen
			held, _ := isolatedSaveReplace(t, path, victim, "parent-directory")
			a.confirmYes()
			isolatedSaveAssertBytes(t, path, "replacement\n")
			isolatedSaveAssertBytes(t, held, "AAA\n")
			isolatedSaveAssertBytes(t, victim, "BBB\n")
			if route == "menu" && pending {
				t.Fatal("delete confirmation opened in isolated pane")
			}
			want := "Delete file" // refused when the menu row fires
			if route == "pending" {
				want = "Delete" // refused at doDeletePath, the final execution point
			}
			isolatedMutatorRefused(t, a, want)
			if len(a.tabs) != 1 {
				t.Fatal("refused delete closed the bound tab")
			}
		})
	}
}

// TestIsolatedFileOpsRefused covers every create/rename/delete entry point:
// menu rows, tree context handlers, and the final do* execution points.
func TestIsolatedFileOpsRefused(t *testing.T) {
	a, path, victim := isolatedMutatorApp(t)
	dir := filepath.Dir(path)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Tree context handlers: isolated panes have no tree, but the handlers
	// refuse before touching it should one ever be reachable.
	node := &filetree.Node{Path: sub, Name: "sub", IsDir: true}
	for label, handler := range map[string]func(*App, *filetree.Node){"New file": ctxNewFile, "Rename": ctxRename, "Delete": ctxDelete} {
		a.statusMsg = ""
		handler(a, node)
		isolatedMutatorRefused(t, a, label)
		if a.promptOpen || a.confirmOpen {
			t.Fatalf("%s context modal opened in isolated pane", label)
		}
	}
	for _, label := range []string{"New file", "Rename file"} {
		if label == "New file" {
			a.menuNewFile() // dynamic label row; Esc n reaches the same method
		} else {
			isolatedPublisherMenu(t, a, label)
		}
		if a.promptOpen {
			a.promptValue = []rune("created.txt")
			a.promptSubmit()
			t.Errorf("%s prompt opened in isolated pane", label)
		}
	}
	a.statusMsg = ""
	a.menuNewFile()
	isolatedMutatorRefused(t, a, "New file")
	a.menuRename()
	isolatedMutatorRefused(t, a, "Rename file")
	a.setActiveFolder(sub)
	a.menuRenameFolder()
	isolatedMutatorRefused(t, a, "Rename folder")
	a.menuDeleteFolder()
	isolatedMutatorRefused(t, a, "Delete folder")
	if a.promptOpen || a.confirmOpen {
		t.Fatal("folder modal opened in isolated pane")
	}

	a.doCreateFile(dir, "created.txt")
	isolatedMutatorRefused(t, a, "New file")
	a.doRenameFile(path, "renamed.txt")
	isolatedMutatorRefused(t, a, "Rename")
	a.doRenameFolder(sub, "moved")
	isolatedMutatorRefused(t, a, "Rename folder")
	a.doDeletePath(sub)
	isolatedMutatorRefused(t, a, "Delete")

	for _, gone := range []string{"created.txt", "renamed.txt", "moved"} {
		if _, err := os.Lstat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s exists after refused file operation: %v", gone, err)
		}
	}
	if info, err := os.Stat(sub); err != nil || !info.IsDir() {
		t.Fatalf("sub removed: %v", err)
	}
	isolatedSaveAssertBytes(t, path, "AAA\n")
	isolatedSaveAssertBytes(t, victim, "BBB\n")
	if len(a.tabs) != 1 || a.tabs[0].Path != path {
		t.Fatal("refused file operation retargeted the bound tab")
	}
}

// TestIsolatedCustomActionRefused: user shell receives $FILE as a pathname,
// which Explorr cannot bind, so linked panes refuse it before and after prompts.
func TestIsolatedCustomActionRefused(t *testing.T) {
	a, path, _ := isolatedMutatorApp(t)
	marker := filepath.Join(filepath.Dir(path), "marker")
	act := customactions.Action{Label: "Touch", Command: "printf x > " + marker + "; printf y >> \"$FILE\""}
	a.customActions = []customactions.Action{act}
	a.runCustomAction(0)
	isolatedMutatorRefused(t, a, "Touch")
	a.statusMsg = ""
	a.execCustomAction(act, nil)
	isolatedMutatorRefused(t, a, "Touch")
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("custom action ran in isolated pane: %v", err)
	}
	isolatedSaveAssertBytes(t, path, "AAA\n")
}

// TestIsolatedFormatterPromptsRefused: the pathname formatter, its trust and
// install prompts are unreachable in isolated panes even when called directly.
func TestIsolatedFormatterPromptsRefused(t *testing.T) {
	a, path, _ := isolatedMutatorApp(t)
	marker := filepath.Join(filepath.Dir(path), "fmt-marker")
	a.execFormatter(path, []string{"sh", "-c", "printf x > " + marker})
	a.openFormatTrustPrompt(0, nil, []string{"true"})
	a.openFormatInstallPrompt(0, "txt", []string{"true"})
	if a.confirmOpen {
		t.Fatal("formatter prompt opened in isolated pane")
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("formatter ran in isolated pane: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(path), ".explorr")); !os.IsNotExist(err) {
		t.Fatalf("formatter config written in isolated pane: %v", err)
	}
}

// TestOrdinaryMutatorsStillWork is the counterexample: ordinary editors keep
// rename symbol, fix at cursor, file operations and custom actions.
func TestOrdinaryMutatorsStillWork(t *testing.T) {
	a := seedNavApp(t, "old := 1\n")
	tab := a.activeTabPtr()
	a.handleRename(&renameEvent{edits: isolatedMutatorEdit(tab.Path, "neu"), newName: "neu", when: time.Now()})
	if got, _ := os.ReadFile(tab.Path); string(got) != "neu := 1\n" || tab.Buffer.String() != "neu := 1\n" {
		t.Fatalf("ordinary rename did not apply: %q / %q (%s)", got, tab.Buffer.String(), a.statusMsg)
	}
	a.handleCodeActions(&codeActionsEvent{actions: []lsp.CodeAction{{Title: "Fix it", Edits: isolatedMutatorEdit(tab.Path, "fix")}}, when: time.Now()})
	if !a.paletteOpen {
		t.Fatal("ordinary fix list did not open")
	}
	a.paletteSelected = 0
	a.runSelectedPaletteCommand()
	if got, _ := os.ReadFile(tab.Path); string(got) != "fix := 1\n" {
		t.Fatalf("ordinary fix did not apply: %q (%s)", got, a.statusMsg)
	}

	dir := filepath.Dir(tab.Path)
	a.menuNewFile()
	if !a.promptOpen {
		t.Fatal("ordinary New file prompt missing")
	}
	a.promptValue = []rune("made.txt")
	a.promptSubmit()
	made := filepath.Join(dir, "made.txt")
	if _, err := os.Stat(made); err != nil {
		t.Fatalf("ordinary create failed: %v (%s)", err, a.statusMsg)
	}
	a.doRenameFile(made, "moved.txt")
	moved := filepath.Join(dir, "moved.txt")
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("ordinary rename failed: %v (%s)", err, a.statusMsg)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a.doRenameFolder(sub, "sub2")
	if _, err := os.Stat(filepath.Join(dir, "sub2")); err != nil {
		t.Fatalf("ordinary folder rename failed: %v", err)
	}
	a.activeTab = len(a.tabs) - 1
	a.menuDelete()
	if !a.confirmOpen {
		t.Fatal("ordinary delete confirmation missing")
	}
	a.confirmYes()
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatalf("ordinary delete failed: %v (%s)", err, a.statusMsg)
	}

	marker := filepath.Join(dir, "marker")
	a.customActions = []customactions.Action{{Label: "Touch", Command: "printf x > " + marker}}
	a.runCustomAction(0)
	if strings.HasPrefix(a.statusMsg, isolatedRefusalPrefix) {
		t.Fatalf("ordinary custom action refused: %q", a.statusMsg)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ordinary custom action did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
