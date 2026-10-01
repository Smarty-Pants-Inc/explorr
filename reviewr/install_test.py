# Copyright (c) 2026 Smarty Pants, Inc. SPDX-License-Identifier: MIT
"""Probe installer ordering and failure paths without network or a Rust build."""

import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent
FAKE_CURL = '''#!/usr/bin/env python3
import json, os, shutil, sys
with open(os.environ['INSTALL_LOG'], 'a') as log:
    log.write(json.dumps(['curl', *sys.argv[1:]]) + '\\n')
shutil.copyfile(os.environ['FIXTURE_ARCHIVE'], sys.argv[sys.argv.index('--output') + 1])
'''
FAKE_CARGO = '''#!/usr/bin/env python3
import json, os, pathlib, sys
with open(os.environ['INSTALL_LOG'], 'a') as log:
    log.write(json.dumps(['cargo', *sys.argv[1:]]) + '\\n')
assert pathlib.Path('marker.txt').read_text() == 'patched\\n', 'patch must run before cargo'
if os.environ.get('FAIL_CARGO'):
    sys.exit(12)
target = pathlib.Path(sys.argv[sys.argv.index('--target-dir') + 1]) / 'release/herdr-reviewr'
target.parent.mkdir(parents=True)
target.write_text('#!/bin/sh\\nexit 0\\n')
target.chmod(0o755)
'''


class InstallTests(unittest.TestCase):
    """Use a fixture pin only in a temporary copy; production pin stays fixed."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='reviewr installer ')
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.bundle = self.base / 'reviewr bundle'
        self.bundle.mkdir()
        for name in ['install.sh', 'upstream.json', 'open-file']:
            shutil.copy2(ROOT / name, self.bundle / name)
        (self.bundle / 'edit-original').symlink_to('open-file')
        self.tools = self.base / 'tools'
        self.tools.mkdir()
        self.write_executable(self.tools / 'curl', FAKE_CURL)
        self.write_executable(self.tools / 'cargo', FAKE_CARGO)
        self.write_executable(self.tools / 'rustc', '#!/bin/sh\nprintf "rustc 1.98.1 (fixture)\\n"\n')
        self.archive = self.base / 'fixture.tar.gz'
        with tarfile.open(self.archive, 'w:gz') as archive:
            for name, content in [('Cargo.lock', '# fixture lock\n'), ('marker.txt', 'original\n')]:
                data = content.encode()
                info = tarfile.TarInfo('herdr-reviewr-0.30.1/' + name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
        pin = json.loads((self.bundle / 'upstream.json').read_text())
        pin['sha256'] = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        (self.bundle / 'upstream.json').write_text(json.dumps(pin))
        (self.bundle / 'upstream.patch').write_text(
            '--- a/marker.txt\n+++ b/marker.txt\n@@ -1 +1 @@\n-original\n+patched\n')
        self.tmpdir = self.base / 'scratch'
        self.tmpdir.mkdir()
        self.log = self.base / 'install.jsonl'
        self.env = dict(os.environ, PATH=str(self.tools) + os.pathsep + os.environ['PATH'],
                        TMPDIR=str(self.tmpdir), FIXTURE_ARCHIVE=str(self.archive),
                        INSTALL_LOG=str(self.log))
        self.env.pop('FAIL_CARGO', None)

    def write_executable(self, path, text):
        """Install a deterministic tool stub into this test's private PATH."""
        path.write_text(text)
        path.chmod(0o755)

    def invoke(self, installer=None):
        """Execute the real shell recipe against the fixture archive/toolchain."""
        return subprocess.run(['sh', str(installer or self.bundle / 'install.sh')],
                              env=self.env, capture_output=True, text=True)

    def calls(self):
        """Read only tool receipts, not installer output heuristics."""
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_verified_archive_patch_locked_build_and_staged_helpers(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertEqual(calls[1][:5], ['cargo', 'build', '--locked', '--release', '--target-dir'])
        self.assertEqual(Path(calls[1][5]).parent.parent, self.tmpdir)
        self.assertEqual(Path(calls[1][5]).name, 'target')
        pin = json.loads((ROOT / 'upstream.json').read_text())
        self.assertEqual(calls[0][-1], pin['archive_url'])
        for name, target in [('herdr-review-last-markdown', 'open-file'),
                             ('herdr-review-edit-original', 'edit-original')]:
            link = self.bundle / 'bin' / name
            self.assertTrue(link.is_symlink())
            self.assertEqual(link.resolve(), (self.bundle / target).resolve())
            self.assertTrue(os.access(link, os.X_OK))
        self.assertTrue(os.access(self.bundle / 'bin' / 'herdr-reviewr', os.X_OK))
        self.assertEqual(list(self.tmpdir.iterdir()), [])
        self.assertEqual(len(calls), 2)  # No herdr plugin/activation call.

    def test_external_cargo_target_dir_does_not_change_isolated_artifact_lookup(self):
        shared = self.base / 'external shared target'
        self.env['CARGO_TARGET_DIR'] = str(shared)
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(shared.exists())
        self.assertTrue((self.bundle / 'bin' / 'herdr-reviewr').exists())
        self.assertEqual(list(self.tmpdir.iterdir()), [])

    def test_checksum_mismatch_prevents_extraction_patch_and_build(self):
        self.archive.write_bytes(b'not an archive')
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('checksum mismatch', result.stderr)
        self.assertEqual(len(self.calls()), 1)
        self.assertFalse((self.bundle / 'bin').exists())
        self.assertEqual(list(self.tmpdir.iterdir()), [])

    def test_patch_failure_never_builds_or_overwrites_binary(self):
        (self.bundle / 'upstream.patch').write_text('invalid patch\n')
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 1)
        self.assertFalse((self.bundle / 'bin').exists())
        self.assertEqual(list(self.tmpdir.iterdir()), [])

    def test_build_failure_preserves_previous_staged_binary(self):
        (self.bundle / 'bin').mkdir()
        binary = self.bundle / 'bin' / 'herdr-reviewr'
        binary.write_text('previous build')
        self.env['FAIL_CARGO'] = '1'
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(binary.read_text(), 'previous build')
        self.assertEqual(list(self.tmpdir.iterdir()), [])

    def test_missing_patch_or_helper_is_checked_before_download(self):
        patch = self.bundle / 'upstream.patch'
        data = patch.read_text()
        patch.unlink()
        self.assertNotEqual(self.invoke().returncode, 0)
        self.assertEqual(self.calls(), [])
        patch.write_text(data)
        (self.bundle / 'edit-original').unlink()
        self.assertNotEqual(self.invoke().returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_old_rust_is_checked_before_download(self):
        self.write_executable(self.tools / 'rustc', '#!/bin/sh\necho "rustc 1.96.0"\n')
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Rust >= 1.97', result.stderr)
        self.assertEqual(self.calls(), [])

    def test_symlink_installer_resolves_own_bundle(self):
        launcher = self.base / 'symlink installer'
        launcher.symlink_to(self.bundle / 'install.sh')
        result = self.invoke(installer=launcher)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.bundle / 'bin' / 'herdr-reviewr').exists())
        self.assertFalse((self.base / 'bin').exists())

    def test_production_pin_and_license_are_mechanically_fixed(self):
        pin = json.loads((ROOT / 'upstream.json').read_text())
        self.assertEqual(pin['tag'], 'v0.30.1')
        self.assertEqual(pin['archive_url'],
                         'https://codeload.github.com/persiyanov/herdr-reviewr/tar.gz/refs/tags/v0.30.1')
        self.assertEqual(pin['sha256'],
                         'a34e286da3defb9edcf52ed44d1178c591ca443a99c63e1d4101505712b15694')
        self.assertIn('Copyright (c) 2026 Dmitry Persiyanov', (ROOT / 'LICENSE').read_text())
        self.assertIn(pin['sha256'], (ROOT / 'NOTICE').read_text())
        self.assertIn('Permission is hereby granted', (ROOT / 'LICENSE').read_text())


if __name__ == '__main__':
    unittest.main()
