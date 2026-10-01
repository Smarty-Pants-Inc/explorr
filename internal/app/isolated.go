// =============================================================================
// File: internal/app/isolated.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"fmt"

	"github.com/Smarty-Pants-Inc/explorr/internal/editor"
)

// isolatedRefusalPrefix starts the flash shown when a linked single-file pane
// refuses a workspace-wide or file-system mutation.
const isolatedRefusalPrefix = "Not available in a linked single-file editor: "

// refuseIsolated reports (and flashes) whether action must be refused because
// this is an isolated link/edit pane. Such a pane edits ONE bound file through
// Tab.Save; pathname writers (LSP workspace edits, create/rename/delete, user
// shell, formatters) could be redirected by a namespace swap, so they are
// refused outright rather than individually bound. Call it at request time
// AND at the final execution point, so a modal or async result already
// pending when the action fires is still refused.
func (a *App) refuseIsolated(action string) bool {
	if !a.isolated {
		return false
	}
	a.flash(isolatedRefusalPrefix + action)
	return true
}

// checkExpectedParent requires t's held parent descriptor to have identity want.
func checkExpectedParent(t *editor.Tab, want editor.ParentID) error {
	got, err := t.BoundParentID()
	if err != nil {
		return fmt.Errorf("cannot verify parent directory: %w", err)
	}
	if got != want {
		return fmt.Errorf("parent directory identity %s does not match expected %s", got, want)
	}
	return nil
}
