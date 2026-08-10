// =============================================================================
// File: main.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Command explorr is Explorr — an opinionated, mouse-first terminal code editor.
// It is designed for the SSH-into-a-box workflow: a single static binary,
// drop it on the remote host, run it inside tmux/zellij, and you get a
// VS-Code-shaped UI (file tree, tabs, syntax highlighting, status bar) you
// can drive almost entirely with the mouse.
package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Smarty-Pants-Inc/explorr/internal/app"
	"github.com/Smarty-Pants-Inc/explorr/internal/state"
	"github.com/Smarty-Pants-Inc/explorr/internal/version"
)

// cliAction is the high-level decision the arg parser hands back: edit
// (start the editor), version (print and exit), or help (print and exit).
// Pulling this out of main keeps the arg-resolution pure and testable
// without dragging in tcell.
type cliAction string

const (
	actionEdit      cliAction = "edit"
	actionExplorer  cliAction = "explorer"
	actionVersion   cliAction = "version"
	actionHelp      cliAction = "help"
	actionOpenAt    cliAction = "open-at"
	actionHerdROpen cliAction = "herdr-open"
	actionHerdR     cliAction = "herdr"
	actionDebug     cliAction = "debug"
)

// cliResult bundles everything resolveArgs hands back: which top-level
// action to run, where to root the editor, which file (if any) to open
// in the first tab, and any user-facing error to surface before exit.
type cliResult struct {
	Action   cliAction
	RootDir  string
	OpenFile string // empty when no file was named (or for non-edit actions)
	OpenLine int
	OpenCol  int

	// DebugAction and HerdRAction hold their validated subcommands.
	// Both are empty for every other action.
	DebugAction string
	HerdRAction string

	Err error
}

func positivePosition(raw, name string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func validateExistingFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("expected a file, got directory %q", path)
	}
	return nil
}

func fileURLPosition(values url.Values, name string) (int, error) {
	raw, ok := values[name]
	if !ok {
		return 1, nil
	}
	if len(raw) != 1 || raw[0] == "" {
		return 0, fmt.Errorf("%s must appear once with a positive integer", name)
	}
	return positivePosition(raw[0], name)
}

func parseLocalFileURL(raw string) (string, int, int, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", 0, 0, err
	}
	if parsed.Scheme != "file" || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Host != "" && parsed.Host != "localhost") {
		return "", 0, 0, errors.New("--herdr-open needs a local file URL")
	}
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", 0, 0, err
	}
	if !filepath.IsAbs(path) {
		return "", 0, 0, errors.New("file URL path must be absolute")
	}
	if err := validateExistingFile(path); err != nil {
		return "", 0, 0, err
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", 0, 0, err
	}
	line, err := fileURLPosition(query, "line")
	if err != nil {
		return "", 0, 0, err
	}
	col, err := fileURLPosition(query, "col")
	if err != nil {
		return "", 0, 0, err
	}
	return path, line, col, nil
}

// resolveArgs parses the editor's tiny CLI surface. The argument can be:
//
//   - a flag (--version / -v / --help / -h) → print-and-exit action
//   - a directory path → use as the editor's root
//   - a file path → root at the file's parent dir, open the file in a tab
//   - a missing path → assume "explorr foo.go" means "create foo.go" —
//     same intuition as `vim foo.go` on a non-existent file.
//
// Pure function; no IO beyond os.Stat. Returns a result the caller acts
// on — keeps main() short and lets tests pin behavior without launching
// a real tcell screen.
func resolveArgs(args []string) cliResult {
	if len(args) == 0 {
		return cliResult{Action: actionEdit, RootDir: "."}
	}
	switch args[0] {
	case "--version", "-v", "-V", "version":
		return cliResult{Action: actionVersion}
	case "--help", "-h", "help":
		return cliResult{Action: actionHelp}
	case "herdr":
		if len(args) != 2 {
			return cliResult{Err: errors.New("herdr needs one action: install | check | remove")}
		}
		switch args[1] {
		case "install", "check", "remove":
			return cliResult{Action: actionHerdR, HerdRAction: args[1]}
		default:
			return cliResult{Err: fmt.Errorf("unknown herdr action %q — want install, check, or remove", args[1])}
		}
	case "--explorer":
		if len(args) > 2 {
			return cliResult{Err: errors.New("--explorer accepts at most one directory")}
		}
		root := "."
		if len(args) == 2 {
			root = args[1]
		}
		info, err := os.Stat(root)
		if err != nil {
			return cliResult{Err: err}
		}
		if !info.IsDir() {
			return cliResult{Err: fmt.Errorf("--explorer needs a directory, got %q", root)}
		}
		return cliResult{Action: actionExplorer, RootDir: root}
	case "--open-at":
		// Ask an ALREADY-RUNNING editor to jump to a location, rather than
		// starting a second one. This is the reverse of the active-file
		// contract: panels read active.json to follow the cursor, and write an
		// open-request to move it. It is what lets the Review panel hand a
		// `path:line` from the agent diff straight into a real editor.
		if len(args) < 2 {
			return cliResult{Err: errors.New("--open-at needs a path, optionally as path:line:col")}
		}
		return cliResult{Action: actionOpenAt, OpenFile: args[1]}
	case "--herdr-open":
		if len(args) != 2 {
			return cliResult{Err: errors.New("--herdr-open needs one local file URL")}
		}
		path, line, col, err := parseLocalFileURL(args[1])
		if err != nil {
			return cliResult{Err: err}
		}
		return cliResult{Action: actionHerdROpen, OpenFile: path, OpenLine: line, OpenCol: col}
	case "--single-file-at":
		if len(args) != 4 {
			return cliResult{Err: errors.New("--single-file-at needs a file, line, and column")}
		}
		if err := validateExistingFile(args[1]); err != nil {
			return cliResult{Err: err}
		}
		line, err := positivePosition(args[2], "line")
		if err != nil {
			return cliResult{Err: err}
		}
		col, err := positivePosition(args[3], "column")
		if err != nil {
			return cliResult{Err: err}
		}
		return cliResult{
			Action: actionEdit, RootDir: filepath.Dir(args[1]), OpenFile: args[1],
			OpenLine: line, OpenCol: col,
		}
	case "--debug":
		// Drive an ALREADY-RUNNING editor's debugger. Same mechanism as
		// --open-at, one file over: the Debug panel mirrors the session out of
		// debug-session.json and writes the next step back through here, so the
		// panel never has to speak the debug adapter protocol itself.
		if len(args) < 2 {
			return cliResult{Err: errors.New("--debug needs an action: " +
				strings.Join(state.DebugActions(), " | "))}
		}
		// Refused HERE rather than by the editor, which has nowhere to complain
		// to: a mistyped verb would otherwise be a key that silently did nothing.
		if !state.ValidDebugAction(args[1]) {
			return cliResult{Err: fmt.Errorf("unknown debug action %q — want one of: %s",
				args[1], strings.Join(state.DebugActions(), " | "))}
		}
		res := cliResult{Action: actionDebug, DebugAction: args[1]}
		if len(args) > 2 {
			res.OpenFile = args[2]
		}
		if args[1] == state.DebugActionToggleBreakpoint && res.OpenFile == "" {
			return cliResult{Err: errors.New("--debug toggle-breakpoint needs a location as file:line")}
		}
		return res
	}

	target := args[0]
	info, err := os.Stat(target)
	switch {
	case err == nil && info.IsDir():
		return cliResult{Action: actionEdit, RootDir: target}
	case err == nil:
		// Existing file — root at its parent so the file tree shows
		// useful context, then open the file as the first tab.
		dir := filepath.Dir(target)
		if dir == "" {
			dir = "."
		}
		return cliResult{Action: actionEdit, RootDir: dir, OpenFile: target}
	case os.IsNotExist(err):
		// Missing path — treat as a "new file" intent (same as vim does).
		// The Tab buffer starts empty and is written to disk on first save.
		dir := filepath.Dir(target)
		if dir == "" {
			dir = "."
		}
		return cliResult{Action: actionEdit, RootDir: dir, OpenFile: target}
	default:
		// Real IO error (permissions, EIO, etc.) — surface it instead of
		// silently swallowing it into a "directory not found" later.
		return cliResult{Err: err}
	}
}

// printHelp writes a short usage block to stdout. Kept brief on purpose:
// the editor is itself the help — once running, the ≡ menu lists every
// action.
func printHelp() {
	fmt.Println(`Explorr — opinionated mouse-first terminal code editor.

Usage:
  explorr                         Open the current directory.
  explorr <directory>             Open a project directory.
  explorr <file>                  Open a file (its parent becomes the project root).
  explorr --explorer [directory]  Open HerdR's file-tree-only workspace sidebar.
  explorr --open-at F:L[:C]       Ask a RUNNING editor to jump to that location.
  explorr --debug ACTION          Drive a RUNNING editor's debugger. ACTION is one of
                                  start, continue, next, stepIn, stepOut, pause, stop,
                                  or toggle-breakpoint FILE:LINE.
  explorr herdr install           Install and link Explorr's HerdR plugin.
  explorr herdr check             Verify the installed HerdR plugin.
  explorr herdr remove            Unlink and remove the HerdR plugin.
  explorr --version               Print the version and exit.
  explorr --help                  Print this help and exit.

Once running, click ≡ (top-left), right-click anywhere, or double-tap Esc
for the action menu. See https://github.com/Smarty-Pants-Inc/explorr for
hotkeys and the full feature list.`)
}

// main routes to the action resolveArgs picked. Edit is by far the
// common path; the print-and-exit branches stay tiny and side-effect
// free so a sanity script or CI check can call --version without
// initialising a tcell screen.
func main() {
	res := resolveArgs(os.Args[1:])
	if res.Err != nil {
		fmt.Fprintln(os.Stderr, "explorr:", res.Err)
		os.Exit(1)
	}

	switch res.Action {
	case actionVersion:
		fmt.Println("explorr", version.Version)
		return
	case actionHelp:
		printHelp()
		return
	case actionHerdR:
		message, err := runHerdRPluginCommand(res.HerdRAction)
		if err != nil {
			fmt.Fprintln(os.Stderr, "explorr:", err)
			os.Exit(1)
		}
		fmt.Println(message)
		return
	case actionOpenAt:
		path, line, col := state.SplitLocation(res.OpenFile)
		abs, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "explorr:", err)
			os.Exit(1)
		}
		if err := state.WriteOpenRequest(abs, line, col); err != nil {
			fmt.Fprintln(os.Stderr, "explorr:", err)
			os.Exit(1)
		}
		return
	case actionHerdROpen:
		if err := app.OpenFileInHerdRTab(res.OpenFile, res.OpenLine, res.OpenCol); err != nil {
			fmt.Fprintln(os.Stderr, "explorr:", err)
			os.Exit(1)
		}
		return
	case actionDebug:
		// The location is optional and only toggle-breakpoint uses it, but it
		// goes through the SAME SplitLocation as --open-at rather than a second
		// parser: "file:line" has exactly one correct reading and a panel emits
		// the identical string for both flags.
		var abs string
		line := 0
		if res.OpenFile != "" {
			path, l, _ := state.SplitLocation(res.OpenFile)
			p, err := filepath.Abs(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, "explorr:", err)
				os.Exit(1)
			}
			abs, line = p, l
		}
		if err := state.WriteDebugRequest(res.DebugAction, abs, line); err != nil {
			fmt.Fprintln(os.Stderr, "explorr:", err)
			os.Exit(1)
		}
		return
	}

	// Explorer mode is the workspace-right HerdR integration: one file tree,
	// no internal editor chrome. File activation creates a regular HerdR tab
	// running single-file mode instead.
	var (
		a   *app.App
		err error
	)
	switch {
	case res.Action == actionExplorer:
		a, err = app.NewExplorer(res.RootDir)
	case res.OpenFile != "":
		a, err = app.NewSingleFileAt(res.OpenFile, res.OpenLine, res.OpenCol)
	default:
		a, err = app.New(res.RootDir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "explorr: failed to start:", err)
		os.Exit(1)
	}
	defer a.Close()

	if err := a.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "explorr:", err)
		os.Exit(1)
	}
}
