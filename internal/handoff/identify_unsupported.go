//go:build !linux && !darwin

// =============================================================================
// File: internal/handoff/identify_unsupported.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package handoff

import (
	"errors"
	"os"
)

// identify fails closed where no-follow openat and DEV:INO are unavailable.
func identify(string) (parent, held *os.File, parentID, fileID string, err error) {
	return nil, nil, "", "", errors.New("identity-held handoff is unsupported on this platform")
}
