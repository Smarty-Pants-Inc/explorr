// =============================================================================
// File: install_test.go
// Copyright: 2026 Smarty Pants, Inc. All rights reserved.
// =============================================================================

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Smarty-Pants-Inc/explorr/internal/version"
)

func writeInstallerArchive(t *testing.T, path string, payload []byte) {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "explorr", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeInstallerTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHerdRManagedInstallVerifiesReleaseChecksum(t *testing.T) {
	assets := t.TempDir()
	archiveName := fmt.Sprintf("explorr_%s_darwin_arm64.tar.gz", version.Version)
	archivePath := filepath.Join(assets, archiveName)
	payload := []byte("#!/bin/sh\necho managed explorr\n")
	writeInstallerArchive(t, archivePath, payload)
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	checksums := filepath.Join(assets, "checksums.txt")
	if err := os.WriteFile(checksums, []byte(fmt.Sprintf("%x  %s\n", digest, archiveName)), 0o644); err != nil {
		t.Fatal(err)
	}

	tools := t.TempDir()
	writeInstallerTool(t, tools, "uname", `#!/bin/sh
case "${1:-}" in
  -s) echo Darwin ;;
  -m) echo arm64 ;;
  *) exit 2 ;;
esac
`)
	writeInstallerTool(t, tools, "jq", "#!/bin/sh\nexit 0\n")
	writeInstallerTool(t, tools, "curl", `#!/bin/sh
set -eu
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output|-o) out="$2"; shift 2 ;;
    --*) shift ;;
    *) url="$1"; shift ;;
  esac
done
[ -n "$out" ] && [ -n "$url" ]
cp "$EXPLORR_TEST_ASSETS/${url##*/}" "$out"
`)
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EXPLORR_TEST_ASSETS", assets)

	run := func(installDir string) ([]byte, error) {
		cmd := exec.Command("/bin/sh", "herdr/install.sh")
		cmd.Env = append(os.Environ(), "INSTALL_DIR="+installDir)
		return cmd.CombinedOutput()
	}

	installedDir := t.TempDir()
	if output, err := run(installedDir); err != nil {
		t.Fatalf("managed install failed: %v\n%s", err, output)
	}
	installedPath := filepath.Join(installedDir, "explorr")
	installed, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, payload) {
		t.Fatalf("installed payload = %q, want %q", installed, payload)
	}
	info, err := os.Stat(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("installed binary mode = %v, want executable", info.Mode())
	}

	if err := os.WriteFile(checksums, []byte(strings.Repeat("0", 64)+"  "+archiveName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rejectedDir := filepath.Join(t.TempDir(), "rejected")
	output, err := run(rejectedDir)
	if err == nil || !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("bad checksum returned %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(rejectedDir, "explorr")); !os.IsNotExist(err) {
		t.Fatalf("bad checksum installed a binary: %v", err)
	}
}
