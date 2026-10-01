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
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Smarty-Pants-Inc/explorr/internal/app"
	"github.com/Smarty-Pants-Inc/explorr/internal/editor"
	"github.com/Smarty-Pants-Inc/explorr/internal/handoff"
	"github.com/Smarty-Pants-Inc/explorr/internal/state"
	"github.com/Smarty-Pants-Inc/explorr/internal/toolpath"
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
	Action     cliAction
	RootDir    string
	OpenFile   string // empty when no file was named (or for non-edit actions)
	OpenLine   int
	OpenCol    int
	ReviewFile bool // explicit ?review=1, in addition to Markdown's default route
	Isolated   bool // dedicated --single-file-at launch; ignore shared panel requests
	// ExpectParent, ExpectFile and Handoff are --single-file-at's REQUIRED
	// identity triple (handoff contract v3): the sender-held DEV:INO of the
	// file's parent directory and of the file itself, and the sender's HANDOFF
	// directory the receiver acknowledges in. The pane fails closed otherwise.
	ExpectParent string
	ExpectFile   string
	Handoff      string

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

// localFileHosts mirrors smarty.file-links' empty, localhost, hostname, short
// hostname and FQDN authorities. Only these full authorities are accepted.
func localFileHosts(hostname, fqdn string) map[string]bool {
	hosts := map[string]bool{"": true, "localhost": true}
	for _, host := range []string{hostname, strings.SplitN(hostname, ".", 2)[0], fqdn} {
		if host != "" {
			hosts[strings.ToLower(host)] = true
		}
	}
	return hosts
}

// systemLocalFileHosts resolves only our own hostname, never the untrusted URL
// host. Bound DNS work so a broken local resolver cannot hang a link action.
func systemLocalFileHosts() map[string]bool {
	hostname, _ := os.Hostname()
	fqdn := hostname
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if name, err := net.DefaultResolver.LookupCNAME(ctx, hostname); err == nil {
		fqdn = strings.TrimSuffix(name, ".")
	}
	if !strings.Contains(fqdn, ".") && hostname != "" {
		if addresses, err := net.DefaultResolver.LookupIPAddr(ctx, hostname); err == nil {
			for _, address := range addresses {
				names, err := net.DefaultResolver.LookupAddr(ctx, address.IP.String())
				if err != nil {
					continue
				}
				for _, name := range names {
					if name = strings.TrimSuffix(name, "."); strings.Contains(name, ".") {
						fqdn = name
						return localFileHosts(hostname, fqdn)
					}
				}
			}
		}
	}
	return localFileHosts(hostname, fqdn)
}

// unsafeFileURLText rejects invalid UTF-8 and the C0, DEL and C1 controls
// forbidden by smarty.file-links, including controls in resolved symlink names.
func unsafeFileURLText(text string) bool {
	return !utf8.ValidString(text) || strings.ContainsFunc(text, func(r rune) bool {
		return r < 0x20 || (r >= 0x7f && r <= 0x9f)
	})
}

// fileURLPositionValue accepts ASCII digits only, matching OSC8 #line links.
func fileURLPositionValue(raw, name string) (int, error) {
	if len(raw) == 0 || strings.ContainsFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, fmt.Errorf("%s must be ASCII digits", name)
	}
	return positivePosition(raw, name)
}

// fileURLPosition rejects duplicate parameters instead of choosing a value.
func fileURLPosition(values url.Values, name string) (int, error) {
	raw, ok := values[name]
	if !ok {
		return 1, nil
	}
	if len(raw) != 1 {
		return 0, fmt.Errorf("%s must appear once", name)
	}
	return fileURLPositionValue(raw[0], name)
}

// parseLocalFileURL validates a clicked URL and returns its canonical target,
// position and explicit Reviewr opt-in. Markdown routing is decided separately.
func parseLocalFileURL(raw string) (string, int, int, bool, error) {
	return parseLocalFileURLForHosts(raw, systemLocalFileHosts())
}

// parseLocalFileURLForHosts keeps local-authority validation deterministic in
// tests while sharing exactly the production URL and filesystem checks.
func parseLocalFileURLForHosts(raw string, hosts map[string]bool) (string, int, int, bool, error) {
	if unsafeFileURLText(raw) {
		return "", 0, 0, false, errors.New("file URL has control characters or invalid UTF-8")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", 0, 0, false, err
	}
	// Match the complete authority, not Hostname(): credentials and ports must
	// not turn into an allowed local hostname after parsing.
	if parsed.Scheme != "file" || parsed.Opaque != "" || parsed.User != nil || !hosts[strings.ToLower(parsed.Host)] {
		return "", 0, 0, false, errors.New("--herdr-open needs a local file URL without credentials or ports")
	}
	// url.Parse has already percent-decoded Path once. Do not unescape again:
	// a literal filename containing "%0a" must not become a newline.
	clicked := parsed.Path
	if !filepath.IsAbs(clicked) || unsafeFileURLText(clicked) {
		return "", 0, 0, false, errors.New("file URL path must be absolute and contain no control characters or invalid UTF-8")
	}
	path, err := filepath.EvalSymlinks(clicked)
	if err != nil {
		return "", 0, 0, false, err
	}
	if !filepath.IsAbs(path) || unsafeFileURLText(path) {
		return "", 0, 0, false, errors.New("resolved file URL path is unsafe")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, 0, false, errors.New("file URL target must be a regular file")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", 0, 0, false, err
	}
	if parsed.ForceQuery || strings.HasPrefix(parsed.RawQuery, "&") || strings.HasSuffix(parsed.RawQuery, "&") || strings.Contains(parsed.RawQuery, "&&") {
		return "", 0, 0, false, errors.New("file URL query contains an empty parameter")
	}
	for name := range query {
		if name != "line" && name != "col" && name != "review" {
			return "", 0, 0, false, fmt.Errorf("unsupported file URL query %q", name)
		}
	}
	review := false
	if values, exists := query["review"]; exists {
		if len(values) != 1 || values[0] != "1" {
			return "", 0, 0, false, errors.New("review must appear once as review=1")
		}
		review = true
	}
	line, err := fileURLPosition(query, "line")
	if err != nil {
		return "", 0, 0, false, err
	}
	if strings.Contains(raw, "#") {
		if _, exists := query["line"]; exists {
			return "", 0, 0, false, errors.New("file URL line appears in both query and fragment")
		}
		// Use the encoded spelling so percent-encoded fragment digits are not
		// silently accepted where smarty.file-links requires ASCII digits.
		fragment := parsed.EscapedFragment()
		if len(fragment) > 9 {
			return "", 0, 0, false, errors.New("line fragment must be at most 9 digits")
		}
		line, err = fileURLPositionValue(fragment, "line fragment")
		if err != nil {
			return "", 0, 0, false, err
		}
	}
	col, err := fileURLPosition(query, "col")
	if err != nil {
		return "", 0, 0, false, err
	}
	return path, line, col, review, nil
}

// reviewMarkdownHelper is a deployment-provided contract, NOT an upstream
// Reviewr command. Despite its historical name, it must open the explicit
// canonical filename, line and column, with the handoff contract v3 identity
// triple, supplied as six separate argv values.
const reviewMarkdownHelper = "herdr-review-last-markdown"

var openFileInHerdRSplit = app.OpenFileInHerdRSplit

// runReviewHelper executes the Reviewr helper; tests substitute a recording fake.
var runReviewHelper = func(helper string, args ...string) ([]byte, error) {
	return exec.Command(helper, args...).CombinedOutput()
}

// openHerdRFile routes Markdown and explicit review=1 links to Reviewr. Missing
// or failing helpers fail closed; ordinary non-Markdown links still use Explorr.
// path is the link's single canonical resolution; both routes identify and
// HOLD it and return only after the receiver acknowledged (handoff contract v3).
func openHerdRFile(path string, line, col int, review bool) error {
	if ext := strings.ToLower(filepath.Ext(path)); review || ext == ".md" || ext == ".markdown" {
		// Herdr supplies the plugin root, but does not prepend its bin to PATH.
		// Prefer the bundled patched helper before the global-install paths.
		var helper string
		if root := os.Getenv("HERDR_PLUGIN_ROOT"); root != "" {
			helper, _ = exec.LookPath(filepath.Join(root, "bin", reviewMarkdownHelper))
		}
		if helper == "" {
			helper, _ = exec.LookPath(reviewMarkdownHelper)
		}
		if helper == "" {
			helper = toolpath.Look(reviewMarkdownHelper)
		}
		if helper == "" {
			return fmt.Errorf("Reviewr requires deployment-provided helper %q; no Explorr fallback", reviewMarkdownHelper)
		}
		return handoff.Send(path, func(s *handoff.Sender) error {
			output, err := runReviewHelper(helper, s.File, strconv.Itoa(line), strconv.Itoa(col), s.ParentID, s.FileID, s.Dir)
			if err != nil {
				return fmt.Errorf("Reviewr helper %q failed: %w: %s", reviewMarkdownHelper, err, strings.TrimSpace(string(output)))
			}
			return nil
		})
	}
	return openFileInHerdRSplit(path, line, col)
}

// The handoff contract v3 options. All three trail --single-file-at FILE LINE
// COL, in any order, each exactly once.
const (
	expectParentFlag = "--expect-parent"
	expectFileFlag   = "--expect-file"
	handoffFlag      = "--handoff"
)

// singleFileAtUsage is the refusal for any malformed --single-file-at form.
const singleFileAtUsage = "--single-file-at needs FILE LINE COL " +
	expectParentFlag + " DEV:INO " + expectFileFlag + " DEV:INO " + handoffFlag + " DIR"

// parseHandoffTriple accepts exactly the three identity options (any order,
// each once) and nothing else; a missing, duplicate or extra word, a malformed
// DEV:INO or a non-absolute/unnormalized HANDOFF is an error. IDs are
// returned normalised.
func parseHandoffTriple(rest []string) (parent, file, dir string, err error) {
	if len(rest) != 6 {
		return "", "", "", errors.New(singleFileAtUsage + " (all three options are required)")
	}
	seen := map[string]string{}
	for i := 0; i < len(rest); i += 2 {
		flag, value := rest[i], rest[i+1]
		if flag != expectParentFlag && flag != expectFileFlag && flag != handoffFlag {
			return "", "", "", errors.New(singleFileAtUsage)
		}
		if _, dup := seen[flag]; dup {
			return "", "", "", fmt.Errorf("%s given twice; %s", flag, singleFileAtUsage)
		}
		if flag == handoffFlag {
			if err := handoff.CheckPath(value); err != nil {
				return "", "", "", fmt.Errorf("%s: %w", flag, err)
			}
			seen[flag] = value
			continue
		}
		id, err := editor.ParseParentID(value)
		if err != nil {
			return "", "", "", fmt.Errorf("%s: %w", flag, err)
		}
		seen[flag] = id.String()
	}
	return seen[expectParentFlag], seen[expectFileFlag], seen[handoffFlag], nil
}

// resolveArgs parses the editor's tiny CLI surface. The argument can be:
//
//   - a flag (--version / -v / --help / -h) → print-and-exit action
//   - a directory path → use as the editor's root
//   - a file path → root at the file's parent dir, open the file in a tab
//   - a missing path → assume "explorr foo.go" means "create foo.go" —
//     same intuition as `vim foo.go` on a non-existent file.
//
// Returns a result the caller acts on; file-link validation also resolves local
// host aliases and symlink targets. Tests pin behavior without launching
// a real tcell screen.
func resolveArgs(args []string) cliResult {
	if len(args) == 0 {
		return cliResult{Action: actionEdit, RootDir: "."}
	}
	// The identity triple qualifies only a dedicated --single-file-at launch; on
	// any other action it would be silently ignored, so it is refused instead.
	if args[0] != "--single-file-at" {
		for _, arg := range args {
			if arg == expectParentFlag || arg == expectFileFlag || arg == handoffFlag {
				return cliResult{Err: errors.New(arg + " is only valid after --single-file-at FILE LINE COL")}
			}
		}
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
		path, line, col, review, err := parseLocalFileURL(args[1])
		if err != nil {
			return cliResult{Err: err}
		}
		return cliResult{Action: actionHerdROpen, OpenFile: path, OpenLine: line, OpenCol: col, ReviewFile: review}
	case "--single-file-at":
		// FILE LINE COL plus the REQUIRED triple; the bare form is refused.
		if len(args) < 4 {
			return cliResult{Err: errors.New(singleFileAtUsage)}
		}
		expectParent, expectFile, handoffDir, err := parseHandoffTriple(args[4:])
		if err != nil {
			return cliResult{Err: err}
		}
		// FILE is the sender's single resolution: never stat'ed or resolved
		// here; the receiver binds it no-follow relative to its parent.
		if err := handoff.CheckPath(args[1]); err != nil {
			return cliResult{Err: err}
		}
		line, lineErr := positivePosition(args[2], "line")
		if lineErr != nil {
			return cliResult{Err: lineErr}
		}
		col, colErr := positivePosition(args[3], "column")
		if colErr != nil {
			return cliResult{Err: colErr}
		}
		return cliResult{
			Action: actionEdit, RootDir: filepath.Dir(args[1]), OpenFile: args[1],
			OpenLine: line, OpenCol: col, Isolated: true,
			ExpectParent: expectParent, ExpectFile: expectFile, Handoff: handoffDir,
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
  explorr --herdr-open FILE_URL  Open a local regular file in the originating HerdR workspace.
                                  Positions: #LINE or ?line=N&col=N; ?review=1 opts into Reviewr.
                                  Markdown always requires the deployment-provided Reviewr helper.
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
		if err := openHerdRFile(res.OpenFile, res.OpenLine, res.OpenCol, res.ReviewFile); err != nil {
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
	// no internal editor chrome. File activation creates a right split running
	// single-file mode beside the active tiled pane.
	var (
		a   *app.App
		err error
	)
	switch {
	case res.Action == actionExplorer:
		a, err = app.NewExplorer(res.RootDir)
	case res.Isolated:
		// resolveArgs never yields Isolated without the full triple.
		a, err = app.NewIsolatedSingleFileAtExpecting(res.OpenFile, res.OpenLine, res.OpenCol, res.ExpectParent, res.ExpectFile, res.Handoff)
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
