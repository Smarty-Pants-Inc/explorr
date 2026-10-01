// =============================================================================
// File: reviewr_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReviewrBundleWithoutPluginPATH reproduces Herdr's real link-action
// bootstrap: the plugin root is supplied, but its bin is absent from PATH.
func TestReviewrBundleWithoutPluginPATH(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "opens-bundle", true: "bundle-failure-is-not-fallback"}[fails], func(t *testing.T) {
			root, global := t.TempDir(), t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "argv")
			script := "#!/bin/sh\n" +
				"test \"$HERDR_PANE_ID\" = wSource:p1 || exit 9\n" +
				"printf '%s\\n' \"$@\" > \"$REVIEWR_TEST_ARGS\"\n"
			if fails {
				script += "echo bundled-reviewr-error >&2\nexit 7\n"
			}
			if err := os.WriteFile(filepath.Join(bin, reviewMarkdownHelper), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(global, reviewMarkdownHelper), []byte("#!/bin/sh\necho wrong-global-helper >&2\nexit 8\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_PLUGIN_ROOT", root)
			t.Setenv("HERDR_PANE_ID", "wSource:p1")
			t.Setenv("PATH", global)
			t.Setenv("REVIEWR_TEST_ARGS", marker)
			previous := openFileInHerdRSplit
			openFileInHerdRSplit = func(string, int, int) error {
				t.Fatal("Reviewr must not fall back to Explorr")
				return nil
			}
			t.Cleanup(func() { openFileInHerdRSplit = previous })
			file := filepath.Join(t.TempDir(), "original space:日本.md")
			err := openHerdRFile(file, 3, 2, false)
			if fails {
				if err == nil || !strings.Contains(err.Error(), "bundled-reviewr-error") {
					t.Fatalf("bundle failure: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(marker)
			if err != nil || string(args) != file+"\n3\n2\n" {
				t.Fatalf("helper argv = %q, %v", args, err)
			}
		})
	}
}
