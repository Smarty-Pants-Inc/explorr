// =============================================================================
// File: internal/app/isolated_helper_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package app

// NewIsolatedSingleFileAt is a TEST-ONLY isolated pane without the handoff
// triple: production --single-file-at always uses
// NewIsolatedSingleFileAtExpecting. It lets the isolation tests exercise
// the pane's refusals without an identity handoff.
func NewIsolatedSingleFileAt(filePath string, line, col int) (*App, error) {
	return newSingleFileAt(filePath, line, col, true, nil)
}
