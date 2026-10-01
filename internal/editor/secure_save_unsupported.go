//go:build !linux && !darwin

// =============================================================================
// File: internal/editor/secure_save_unsupported.go
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"fmt"
	"os"
)

// bindOriginal fails closed where the required POSIX descriptor API is absent.
func bindOriginal(string, os.FileInfo, string) (*boundOriginal, error) {
	return nil, fmt.Errorf("bound-original saving is unsupported on this platform")
}

// save is unreachable after binding fails, but still cannot perform any writes.
func (b *boundOriginal) save(string, []byte) (os.FileInfo, error) {
	return nil, fmt.Errorf("bound-original saving is unsupported on this platform")
}

// reload never falls back to a symlink-following read on unsupported platforms.
func (b *boundOriginal) reload(string, bool) ([]byte, os.FileInfo, error) {
	return nil, nil, fmt.Errorf("bound-original reloading is unsupported on this platform")
}
