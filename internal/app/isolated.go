// =============================================================================
// File: internal/app/isolated.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

import (
	"fmt"

	"github.com/Smarty-Pants-Inc/explorr/internal/editor"
	"github.com/Smarty-Pants-Inc/explorr/internal/handoff"
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

// ReceiveHandoff is the --single-file-at receiver step of handoff contract v3:
// FILE (absolute, normalized, never resolved) is opened relative to its parent
// with O_NOFOLLOW and bound (editor.NewBoundTab), both HELD identities are
// checked against parentID and fileID, and only then is HANDOFF/ack created
// (handoff.Acknowledge) carrying nonce (from this process's argv) and the
// DEV:INO of the descriptor actually bound. Any failure returns no tab: the
// bound descriptors are released and nothing has been written. The caller
// registers the returned tab.
func ReceiveHandoff(filePath, parentID, fileID, handoffDir, nonce string) (*editor.Tab, error) {
	if err := handoff.CheckPath(filePath); err != nil {
		return nil, err
	}
	if err := handoff.CheckPath(handoffDir); err != nil {
		return nil, fmt.Errorf("handoff: %w", err)
	}
	if err := handoff.CheckNonce(nonce); err != nil {
		return nil, err
	}
	parent, err := editor.ParseParentID(parentID)
	if err != nil {
		return nil, fmt.Errorf("expected parent: %w", err)
	}
	file, err := editor.ParseParentID(fileID)
	if err != nil {
		return nil, fmt.Errorf("expected file: %w", err)
	}
	t, err := editor.NewBoundTab(filePath, parent, file)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", filePath, err)
	}
	// Text tabs re-check the identities on the descriptors they keep for Save.
	// Image previews keep none: NewBoundTab already checked both identities on
	// the very descriptors it read the image from, and they never re-read.
	bound := file
	if !t.IsImage() {
		if err := checkExpected(t, parent, file); err != nil {
			_ = t.Close()
			return nil, fmt.Errorf("cannot open %s: %w", filePath, err)
		}
		if bound, err = t.BoundFileID(); err != nil {
			_ = t.Close()
			return nil, fmt.Errorf("cannot open %s: %w", filePath, err)
		}
	}
	if err := handoff.Acknowledge(handoffDir, nonce, bound.String()); err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("cannot open %s: %w", filePath, err)
	}
	return t, nil
}

// checkExpected requires t's held parent descriptor AND its held no-follow
// original descriptor to have exactly the expected identities. Unbound tabs
// (failed binds, read-only previews) have neither and therefore fail.
func checkExpected(t *editor.Tab, wantParent, wantFile editor.ParentID) error {
	gotParent, err := t.BoundParentID()
	if err != nil {
		return fmt.Errorf("cannot verify parent directory: %w", err)
	}
	if gotParent != wantParent {
		return fmt.Errorf("parent directory identity %s does not match expected %s", gotParent, wantParent)
	}
	gotFile, err := t.BoundFileID()
	if err != nil {
		return fmt.Errorf("cannot verify original file: %w", err)
	}
	if gotFile != wantFile {
		return fmt.Errorf("original file identity %s does not match expected %s", gotFile, wantFile)
	}
	return nil
}
