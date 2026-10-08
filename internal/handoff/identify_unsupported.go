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

// ackNoFollow is unavailable here; Acknowledge still uses O_EXCL.
const ackNoFollow = 0

// fileIDOf fails closed: no DEV:INO on this platform.
func fileIDOf(*os.File) (string, error) {
	return "", errors.New("identity-held handoff is unsupported on this platform")
}

// readAck fails closed: no ack can be validated on this platform.
func readAck(string, int64) ([]byte, error) {
	return nil, errors.New("identity-held handoff is unsupported on this platform")
}

// identify fails closed where no-follow openat and DEV:INO are unavailable.
func identify(string) (parent, held *os.File, parentID, fileID string, err error) {
	return nil, nil, "", "", errors.New("identity-held handoff is unsupported on this platform")
}
