package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/filetree"
	"github.com/Smarty-Pants-Inc/explorr/internal/handoff"
	"github.com/Smarty-Pants-Inc/explorr/internal/theme"
)

// NewExplorer builds the HerdR plugin file tree. It deliberately
// omits editor tabs, LSPs, publishers, and the project finder: activating a
// file opens a single-file editor in a right split beside its source pane.
func NewExplorer(rootDir string) (*App, error) {
	th := theme.FromHerdR(theme.Default())
	scr, err := newScreen(th)
	if err != nil {
		return nil, err
	}
	scr.SetStyle(tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Text))
	scr.Clear()

	tree, err := filetree.New(rootDir)
	if err != nil {
		scr.Fini()
		return nil, err
	}
	tree.ExternalHeader = true

	a := &App{
		screen:         scr,
		theme:          th,
		explorer:       true,
		rootDir:        tree.Root.Path,
		tree:           tree,
		hoveredMenuRow: -1,
		sidebarShown:   true,
		sidebarWidth:   defaultSidebarWidth,
	}
	a.setActiveFolder(tree.Root.Path)
	a.loadConfig()
	a.refreshGitStatus()
	a.tree.Focus(tree.Root.Path, 0)
	a.startTreeRefresh()
	return a, nil
}

// openTreeFile opens a clicked tree file. In the explorer this is the explorer
// sender route of handoff contract v3: the tree path is resolved ONCE to its
// canonical FILE, which is identified, held and launched synchronously (so an
// origin or launch failure is shown at once), while the up-to-15 s wait for
// the receiver's ack runs off the UI goroutine and reports back through an
// explorerOpenDoneEvent.
func (a *App) openTreeFile(path string) {
	if !a.explorer {
		a.openFile(path)
		return
	}
	a.tree.ActiveFile = path
	sender, err := startTreeFileInHerdRSplit(path)
	if err != nil {
		a.openInfo("Could not open file", []string{err.Error()})
		return
	}
	scr := a.screen
	go func() {
		err := sender.Await()
		if scr != nil {
			_ = scr.PostEvent(&explorerOpenDoneEvent{when: time.Now(), path: path, err: err})
		}
	}()
}

// startTreeFileInHerdRSplit is the explorer's single resolution step followed
// by the shared sender's identify, hold and launch.
func startTreeFileInHerdRSplit(path string) (*handoff.Sender, error) {
	sourcePaneID := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	if err := checkHerdROrigin(sourcePaneID); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return startFileInHerdRSplit(herdrRunner, canonical, 1, 1, sourcePaneID)
}

// checkHerdROrigin requires an explicit tiled originating pane.
func checkHerdROrigin(sourcePaneID string) error {
	if sourcePaneID == "" {
		return fmt.Errorf("HERDR_PANE_ID is not set; cannot identify the originating pane")
	}
	if strings.HasSuffix(sourcePaneID, ":plugin") {
		return fmt.Errorf("HERDR_PANE_ID %q is a legacy sidebar, not a tiled originating pane", sourcePaneID)
	}
	return nil
}

// explorerOpenDoneEvent carries an explorer launch's acknowledgement result
// back to the UI goroutine.
type explorerOpenDoneEvent struct {
	when time.Time
	path string
	err  error
}

// When satisfies the tcell.Event interface.
func (e *explorerOpenDoneEvent) When() time.Time { return e.when }

// handleExplorerOpenDone shows a failed handoff; success needs no message.
func (a *App) handleExplorerOpenDone(e *explorerOpenDoneEvent) {
	if e.err != nil {
		a.openInfo("Could not open file", []string{filepath.Base(e.path) + ": " + e.err.Error()})
	}
}

type herdrPaneSplitResponse struct {
	Result struct {
		Pane struct {
			PaneID string `json:"pane_id"`
		} `json:"pane"`
	} `json:"result"`
}

// HerdRRunner runs one herdr manager command and returns its output.
type HerdRRunner func(bin string, args ...string) ([]byte, error)

// herdrRunner is the production manager; tests substitute a recording fake.
var herdrRunner HerdRRunner = runHerdR

// OpenFileInHerdRSplit opens a single-file editor beside the originating pane.
// HerdR injects HERDR_PANE_ID for both link actions and ordinary plugin panes.
// path is the sender's already resolved FILE (absolute, normalized); it is not
// resolved again. It returns only after the receiver acknowledged (or the
// handoff failed), so --herdr-open holds the file until then.
func OpenFileInHerdRSplit(path string, line, col int) error {
	return OpenFileInHerdRSplitWith(herdrRunner, path, line, col)
}

// OpenFileInHerdRSplitWith is OpenFileInHerdRSplit through an explicit
// manager runner: identify and hold FILE, launch the receiver with the
// identity triple, then wait for its ack and run the close steps.
func OpenFileInHerdRSplitWith(run HerdRRunner, path string, line, col int) error {
	sender, err := startFileInHerdRSplit(run, path, line, col, strings.TrimSpace(os.Getenv("HERDR_PANE_ID")))
	if err != nil {
		return err
	}
	return sender.Await()
}

// startFileInHerdRSplit requires an explicit tiled origin; missing or legacy
// sidebar origins must never fall back to another client's focused pane. It
// identifies and HOLDS FILE (handoff.Identify) before launching
// `--single-file-at FILE LINE COL --expect-parent P --expect-file F --handoff H`
// in a new split; a launch failure runs the handoff close steps. On success
// the caller must Await the returned sender.
func startFileInHerdRSplit(run HerdRRunner, path string, line, col int, sourcePaneID string) (*handoff.Sender, error) {
	if err := checkHerdROrigin(sourcePaneID); err != nil {
		return nil, err
	}
	herdrBin := strings.TrimSpace(os.Getenv("HERDR_BIN_PATH"))
	if herdrBin == "" {
		herdrBin = "herdr"
	}
	if err := handoff.CheckPath(path); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	sender, err := handoff.Identify(path)
	if err != nil {
		return nil, err
	}
	if err := launchHerdRSplit(run, herdrBin, executable, sender, line, col, sourcePaneID); err != nil {
		return nil, sender.Abort(err)
	}
	return sender, nil
}

// launchHerdRSplit creates the unfocused split and runs the receiver in it.
func launchHerdRSplit(run HerdRRunner, herdrBin, executable string, s *handoff.Sender, line, col int, sourcePaneID string) error {
	// The public CLI has no originating-client focus token. Keep focus unchanged
	// rather than changing session focus on behalf of an unrelated client.
	splitArgs := []string{"pane", "split", "--pane", sourcePaneID,
		"--direction", "right",
		"--cwd", filepath.Dir(s.File),
		"--no-focus"}
	output, err := run(herdrBin, splitArgs...)
	if err != nil {
		return err
	}
	var split herdrPaneSplitResponse
	if err := json.Unmarshal(output, &split); err != nil {
		return fmt.Errorf("parse herdr pane split response: %w", err)
	}
	paneID := split.Result.Pane.PaneID
	if paneID == "" {
		return fmt.Errorf("herdr pane split response omitted pane id")
	}

	// Every variable word is quoted; flags and decimal positions are literal.
	command := "exec " + shellQuote(executable) + " --single-file-at " + shellQuote(s.File) +
		" " + fmt.Sprint(max(1, line)) + " " + fmt.Sprint(max(1, col)) +
		" --expect-parent " + shellQuote(s.ParentID) + " --expect-file " + shellQuote(s.FileID) +
		" --handoff " + shellQuote(s.Dir)
	if _, err := run(herdrBin, "pane", "run", paneID, command); err != nil {
		_, _ = run(herdrBin, "pane", "close", paneID)
		return err
	}
	return nil
}

func runHerdR(bin string, args ...string) ([]byte, error) {
	output, err := exec.Command(bin, args...).CombinedOutput()
	if err == nil {
		return output, nil
	}
	action := strings.Join(args[:min(2, len(args))], " ")
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return nil, fmt.Errorf("herdr %s: %s", action, detail)
	}
	return nil, fmt.Errorf("herdr %s: %w", action, err)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// menuFocusSidebar enters explorer navigation. If the sidebar was hidden,
// the same gesture restores it first; panes too narrow to show it explain why
// instead of accepting keyboard focus on an invisible surface.
func (a *App) menuFocusSidebar() {
	a.closeMenu()
	if a.tree == nil {
		a.flash("No file explorer in single-file mode")
		return
	}
	a.sidebarShown = true
	if !a.sidebarVisible() {
		a.tree.Blur()
		a.flash(fmt.Sprintf("File explorer is hidden automatically below %d columns (pane is %d)",
			treeNeeds, a.width))
		return
	}
	path := a.tree.ActiveFile
	if path == "" {
		path = a.tree.ActiveFolder
	}
	a.tree.Focus(path, a.treeListHeight())
}

func (a *App) treeListHeight() int {
	_, _, _, h := a.sidebarRect()
	return max(0, h-2)
}

// handleTreeKey implements the same compact navigation vocabulary HerdR uses:
// Esc leaves, arrows plus hjkl move, and Enter activates the selected row.
func (a *App) handleTreeKey(ev *tcell.EventKey) {
	viewH := a.treeListHeight()
	r := ev.Rune()
	switch {
	case ev.Key() == tcell.KeyEsc || ev.Key() == tcell.KeyTab || ev.Key() == tcell.KeyBacktab:
		if !a.explorer {
			a.tree.Blur()
		}
	case ev.Key() == tcell.KeyUp || r == 'k':
		a.tree.MoveSelection(-1, viewH)
	case ev.Key() == tcell.KeyDown || r == 'j':
		a.tree.MoveSelection(1, viewH)
	case ev.Key() == tcell.KeyHome:
		a.tree.SelectFirst(viewH)
	case ev.Key() == tcell.KeyEnd:
		a.tree.SelectLast(viewH)
	case ev.Key() == tcell.KeyPgUp:
		a.tree.MoveSelection(-max(1, viewH-1), viewH)
	case ev.Key() == tcell.KeyPgDn:
		a.tree.MoveSelection(max(1, viewH-1), viewH)
	case ev.Key() == tcell.KeyLeft || r == 'h':
		n := a.tree.SelectedNode()
		if n != nil && n != a.tree.Root && n.IsDir && n.Expanded {
			a.tree.Toggle(n)
			a.tree.SelectNode(n, viewH)
		} else {
			a.tree.SelectParent(viewH)
		}
	case ev.Key() == tcell.KeyRight || r == 'l':
		n := a.tree.SelectedNode()
		if n == nil || !n.IsDir {
			return
		}
		if !n.Expanded {
			a.tree.Toggle(n)
			a.tree.SelectNode(n, viewH)
			return
		}
		a.tree.SelectFirstChild(viewH)
	case ev.Key() == tcell.KeyEnter:
		a.activateTreeSelection(viewH)
	}
}

func (a *App) activateTreeSelection(viewH int) {
	n := a.tree.SelectedNode()
	if n == nil {
		return
	}
	if n == a.tree.Root {
		a.setActiveFolder(a.rootDir)
		return
	}
	if n.IsDir {
		a.setActiveFolder(n.Path)
		a.tree.Toggle(n)
		a.tree.SelectNode(n, viewH)
		return
	}
	a.setActiveFolder(filepath.Dir(n.Path))
	a.openTreeFile(n.Path)
	if !a.explorer {
		a.tree.Blur()
	}
}

func (a *App) drawTreeNavigateStatus(sx, sy, sw int) {
	base := tcell.StyleDefault.Background(a.theme.StatusBG).Foreground(a.theme.Muted)
	for cx := sx; cx < sx+sw; cx++ {
		a.screen.SetContent(cx, sy, ' ', nil, base)
	}
	mode := tcell.StyleDefault.Background(a.theme.Accent).Foreground(a.theme.BG).Bold(true)
	key := tcell.StyleDefault.Background(a.theme.StatusBG).Foreground(a.theme.Accent).Bold(true)
	dim := tcell.StyleDefault.Background(a.theme.StatusBG).Foreground(a.theme.Muted)
	cx := sx
	put := func(text string, style tcell.Style) {
		for _, r := range text {
			if cx >= sx+sw {
				return
			}
			a.screen.SetContent(cx, sy, r, nil, style)
			cx++
		}
	}
	put(" NAVIGATE ", mode)
	put(" ", dim)
	put("esc", key)
	put(" back  ", dim)
	put("↑/↓ j/k", key)
	put(" move  ", dim)
	put("h/l", key)
	put(" tree  ", dim)
	put("enter", key)
	put(" open  ", dim)
	put("tab", key)
	put(" editor", dim)
}
