package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/Smarty-Pants-Inc/explorr/internal/filetree"
	"github.com/Smarty-Pants-Inc/explorr/internal/theme"
)

// NewExplorer builds the workspace-right HerdR file tree. It deliberately
// omits editor tabs, LSPs, publishers, and the project finder: activating a
// file opens a separate single-file editor in a regular HerdR tab.
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

func (a *App) openTreeFile(path string) {
	if !a.explorer {
		a.openFile(path)
		return
	}
	a.tree.ActiveFile = path
	if err := OpenFileInHerdRTab(path, 1, 1); err != nil {
		a.openInfo("Could not open file", []string{err.Error()})
	}
}

type herdrTabCreateResponse struct {
	Result struct {
		Tab struct {
			TabID string `json:"tab_id"`
		} `json:"tab"`
		RootPane struct {
			PaneID string `json:"pane_id"`
		} `json:"root_pane"`
	} `json:"result"`
}

func OpenFileInHerdRTab(path string, line, col int) error {
	workspaceID := strings.TrimSpace(os.Getenv("HERDR_WORKSPACE_ID"))
	if workspaceID == "" {
		return fmt.Errorf("HERDR_WORKSPACE_ID is not set")
	}
	herdrBin := strings.TrimSpace(os.Getenv("HERDR_BIN_PATH"))
	if herdrBin == "" {
		herdrBin = "herdr"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	output, err := runHerdR(herdrBin, "tab", "create",
		"--workspace", workspaceID,
		"--cwd", filepath.Dir(abs),
		"--label", filepath.Base(abs),
		"--no-focus")
	if err != nil {
		return err
	}
	var created herdrTabCreateResponse
	if err := json.Unmarshal(output, &created); err != nil {
		return fmt.Errorf("parse herdr tab create response: %w", err)
	}
	tabID := created.Result.Tab.TabID
	paneID := created.Result.RootPane.PaneID
	if tabID == "" || paneID == "" {
		return fmt.Errorf("herdr tab create response omitted tab or pane id")
	}
	cleanup := func() { _, _ = runHerdR(herdrBin, "tab", "close", tabID) }

	executable, err := os.Executable()
	if err != nil {
		cleanup()
		return err
	}
	command := "exec " + shellQuote(executable) + " --single-file-at " + shellQuote(abs) + " " + fmt.Sprint(max(1, line)) + " " + fmt.Sprint(max(1, col))
	if _, err := runHerdR(herdrBin, "pane", "run", paneID, command); err != nil {
		cleanup()
		return err
	}
	if _, err := runHerdR(herdrBin, "tab", "focus", tabID); err != nil {
		cleanup()
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
