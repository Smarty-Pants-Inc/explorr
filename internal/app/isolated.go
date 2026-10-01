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
// shell, formatters, debug builds) could be redirected by a namespace swap, so
// they are refused outright rather than individually bound. Call it at request time
// AND at the final execution point, so a modal or async result already
// pending when the action fires is still refused.
func (a *App) refuseIsolated(action string) bool {
	if !a.isolated {
		return false
	}
	a.flash(isolatedRefusalPrefix + action)
	return true
}

// isolatedExpectation is the caller-verified identity pair (Reviewr's edit
// helper) the initial isolated tab must HOLD: its parent directory and the
// original file itself, both decimal DEV:INO. The parent alone cannot stop a
// same-folder name swap (A.md -> B.md); the file identity can.
type isolatedExpectation struct {
	parent editor.ParentID
	file   editor.ParentID
}

// parseIsolatedExpectation parses both identities; either malformed fails.
func parseIsolatedExpectation(parentID, fileID string) (*isolatedExpectation, error) {
	parent, err := editor.ParseParentID(parentID)
	if err != nil {
		return nil, fmt.Errorf("expected parent: %w", err)
	}
	file, err := editor.ParseParentID(fileID)
	if err != nil {
		return nil, fmt.Errorf("expected file: %w", err)
	}
	return &isolatedExpectation{parent: parent, file: file}, nil
}

// checkExpected requires t's held parent descriptor AND its held no-follow
// original descriptor to have exactly the expected identities. Unbound tabs
// (failed binds, read-only previews) have neither and therefore fail.
func checkExpected(t *editor.Tab, want isolatedExpectation) error {
	gotParent, err := t.BoundParentID()
	if err != nil {
		return fmt.Errorf("cannot verify parent directory: %w", err)
	}
	if gotParent != want.parent {
		return fmt.Errorf("parent directory identity %s does not match expected %s", gotParent, want.parent)
	}
	gotFile, err := t.BoundFileID()
	if err != nil {
		return fmt.Errorf("cannot verify original file: %w", err)
	}
	if gotFile != want.file {
		return fmt.Errorf("original file identity %s does not match expected %s", gotFile, want.file)
	}
	return nil
}
