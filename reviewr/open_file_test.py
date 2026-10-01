# Copyright (c) 2026 Smarty Pants, Inc. SPDX-License-Identifier: MIT
"""Small subprocess probes of the helper's public argv and pane safety contract."""

import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent
FAKE_HERDR = '''#!/usr/bin/env python3
import json, os, subprocess, sys
args = sys.argv[1:]
with open(os.environ['CALL_LOG'], 'a') as log:
    log.write(json.dumps(args) + '\\n')
if args[:2] == ['pane', 'split']:
    if os.environ.get('FAIL_SPLIT'):
        print('stale origin: not found', file=sys.stderr)
        sys.exit(7)
    print(os.environ.get('SPLIT_REPLY', json.dumps({'result': {'pane': {'pane_id': 'wA:p8'}}})))
elif args[:2] == ['pane', 'run']:
    if os.environ.get('FAIL_RUN'):
        print('run stdout detail')
        print('run stderr detail', file=sys.stderr)
        sys.exit(9)
    if os.environ.get('EXECUTE_COMMAND'):
        sys.exit(subprocess.run(['/bin/sh', '-c', args[3]]).returncode)
elif args[:2] == ['pane', 'close'] and os.environ.get('FAIL_CLOSE'):
    print('close failed', file=sys.stderr)
    sys.exit(10)
'''
FAKE_PROGRAM = '''#!/usr/bin/env python3
import json, os, sys
with open(os.environ['PROGRAM_LOG'], 'w') as log:
    json.dump(sys.argv, log)
'''


class OpenFileTests(unittest.TestCase):
    """Exercise installed/symlink entry points, not only internal functions."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='reviewr helper ')
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.bundle = self.base / 'bundle' / 'reviewr'
        (self.bundle / 'bin').mkdir(parents=True)
        shutil.copy2(ROOT / 'open-file', self.bundle / 'open-file')
        (self.bundle / 'edit-original').symlink_to('open-file')
        self.launch = self.base / 'launch'
        self.launch.mkdir()
        (self.launch / 'herdr-review-last-markdown').symlink_to(self.bundle / 'open-file')
        (self.launch / 'herdr-review-edit-original').symlink_to(self.bundle / 'edit-original')
        self.manager = self.base / "herdr ' manager"
        self.write_executable(self.manager, FAKE_HERDR)
        self.reviewr = self.bundle / 'bin' / 'herdr-reviewr'
        self.write_executable(self.reviewr, FAKE_PROGRAM)
        self.explorr = self.bundle.parent / 'herdr' / 'bin' / 'explorr'
        self.explorr.parent.mkdir(parents=True)
        self.write_executable(self.explorr, FAKE_PROGRAM)
        self.file = self.base / 'plain file.md'
        self.file.write_text('# non-Git markdown\nline two\n')
        self.log = self.base / 'calls.jsonl'
        self.program_log = self.base / 'program.json'
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith(('HERDR_', 'EXPLORR_'))}
        self.env.update(HERDR_PANE_ID='wA:p7', HERDR_BIN_PATH=str(self.manager),
                        CALL_LOG=str(self.log), PROGRAM_LOG=str(self.program_log),
                        HERDR_WORKSPACE_ID='wWrong', HERDR_TAB_ID='wWrong:t2')

    def write_executable(self, path, text):
        """Make an inert deterministic CLI fixture."""
        path.write_text(text)
        path.chmod(0o755)

    def invoke(self, file=None, line='2', col='13', edit=False, parent=None):
        """Run the named public helper with the isolated fixture environment.

        Edit mode passes Reviewr's held-parent DEV:INO, by default the real one."""
        name = 'herdr-review-edit-original' if edit else 'herdr-review-last-markdown'
        argv = [str(self.launch / name), str(file or self.file), line, col]
        if edit:
            argv.append(parent or self.parent_id((file or self.file).resolve().parent))
        return subprocess.run(argv, env=self.env, capture_output=True, text=True)

    @staticmethod
    def parent_id(directory):
        info = os.stat(directory)
        return f'{info.st_dev & 0xFFFFFFFFFFFFFFFF}:{info.st_ino}'

    def calls(self):
        """Read exact manager argv, preserving shell metacharacters."""
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_explicit_origin_non_git_file_line_and_no_focus(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls()[0], ['pane', 'split', '--pane', 'wA:p7',
                         '--direction', 'right', '--cwd', str(self.file.parent), '--no-focus'])
        self.assertEqual(self.calls()[1][:3], ['pane', 'run', 'wA:p8'])
        self.assertEqual(shlex.split(self.calls()[1][3]),
                         ['exec', str(self.reviewr), '--file', str(self.file), '--line', '2'])
        self.assertFalse((self.base / '.git').exists())
        self.assertEqual(len(self.calls()), 2)

    def test_shell_quotes_survive_actual_shell_without_injection(self):
        file = self.base / "- ' $(touch PWNED); `echo bad` & : file.md"
        file.write_text('markdown')
        self.env['EXECUTE_COMMAND'] = '1'
        result = self.invoke(file=file)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.program_log.read_text())[1:],
                         ['--file', str(file), '--line', '2'])
        self.assertFalse((self.base / 'PWNED').exists())
        self.assertFalse((ROOT.parent / 'PWNED').exists())

    def test_canonical_regular_file_and_parent(self):
        link = self.base / 'alias.md'
        link.symlink_to(self.file)
        result = self.invoke(file=link)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(str(self.file), shlex.split(self.calls()[1][3]))

    def test_missing_or_pseudo_origin_never_calls_manager(self):
        for origin in ['', 'current', 'focused', 'wA', 'wA:p1 --current', 'wA:p0\n']:
            with self.subTest(origin=origin):
                self.env['HERDR_PANE_ID'] = origin
                result = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('HERDR_PANE_ID', result.stderr)
                self.assertEqual(self.calls(), [])

    def test_herdr_base32_pane_numbers_past_nine_are_real_panes(self):
        # HerdR 0.9.1 encodes pane 10 as pA (alphabet 1-9, A-Z without I/L/O/U, then 0).
        self.env.update(HERDR_PANE_ID='w1:pA',
                        SPLIT_REPLY=json.dumps({'result': {'pane': {'pane_id': 'w1:pZ1'}}}))
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls()[0][3], 'w1:pA')
        self.assertEqual(self.calls()[1][:3], ['pane', 'run', 'w1:pZ1'])
        for origin in ['w1:pa', 'w1:pI', 'w1:pO', 'w1:pL', 'w1:pU']:
            with self.subTest(origin=origin):
                self.log.unlink(missing_ok=True)
                self.env['HERDR_PANE_ID'] = origin
                result = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.calls(), [])

    def test_stale_origin_has_no_fallback(self):
        self.env['FAIL_SPLIT'] = '1'
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('stale origin', result.stderr)
        self.assertIn(shlex.quote(str(self.manager)), result.stderr)
        self.assertEqual(len(self.calls()), 1)
        self.assertEqual(self.calls()[0][3], 'wA:p7')

    def test_run_failure_closes_only_created_pane_and_reports_both_outputs(self):
        self.env['FAIL_RUN'] = '1'
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls()[2], ['pane', 'close', 'wA:p8'])
        self.assertIn('run stdout detail', result.stderr)
        self.assertIn('run stderr detail', result.stderr)
        self.assertIn('pane run wA:p8', result.stderr)
        self.assertEqual(len(self.calls()), 3)

    def test_cleanup_failure_is_reported_without_retry(self):
        self.env.update(FAIL_RUN='1', FAIL_CLOSE='1')
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('cleanup also failed', result.stderr)
        self.assertEqual(len(self.calls()), 3)

    def test_bad_response_never_runs_or_closes_an_unverified_pane(self):
        for reply in ['not JSON', '{"result":{}}',
                      '{"result":{"pane":{"pane_id":"current"}}}',
                      '{"result":{"pane":{"pane_id":"wA:p7"}}}',
                      '{"result":{"pane":{"pane_id":"wOther:p8"}}}']:
            with self.subTest(reply=reply):
                self.log.unlink(missing_ok=True)
                self.env['SPLIT_REPLY'] = reply
                result = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(self.calls()), 1)

    def test_missing_patched_binary_fails_before_split_no_path_fallback(self):
        self.reviewr.unlink()
        # Even a similarly named executable on PATH must not replace our patch.
        self.write_executable(self.launch / 'herdr-reviewr', FAKE_PROGRAM)
        self.env['PATH'] = str(self.launch) + os.pathsep + self.env['PATH']
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('missing bundled herdr-reviewr', result.stderr)
        self.assertEqual(self.calls(), [])

    def test_edit_launches_native_explorr_not_markdown_handler(self):
        self.env['EXECUTE_COMMAND'] = '1'
        result = self.invoke(edit=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.program_log.read_text()),
                         [str(self.explorr), '--single-file-at', str(self.file), '2', '13',
                          '--expect-parent', self.parent_id(self.file.parent)])
        self.assertNotIn('--open-at', self.calls()[1][3])
        self.assertNotIn('--herdr-open', self.calls()[1][3])

    def test_edit_refuses_replaced_parent_before_split(self):
        # Reviewr verified and holds current/; a new directory now sits at that path
        # with a different same-named document. The helper must not launch it.
        current = self.base / 'current'
        current.mkdir()
        document = current / 'A.md'
        document.write_text('reviewed document')
        held = self.parent_id(current)
        current.rename(self.base / 'moved')
        current.mkdir()
        document.write_text('different document at the same pathname')
        result = self.invoke(file=document, edit=True, parent=held)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('directory was replaced', result.stderr)
        self.assertEqual(self.calls(), [])

    def test_edit_requires_well_formed_parent_identity(self):
        for parent in ['', '1', '1:', ':2', '1:2:3', '-1:2', '1:2\n', '1:2; touch bad']:
            with self.subTest(parent=parent):
                result = subprocess.run([str(self.launch / 'herdr-review-edit-original'),
                                         str(self.file), '2', '13', parent],
                                        env=self.env, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.calls(), [])
        result = subprocess.run([str(self.launch / 'herdr-review-edit-original'),
                                 str(self.file), '2', '13'], env=self.env,
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('PARENT_ID', result.stderr)
        self.assertEqual(self.calls(), [])

    def test_plugin_bin_and_absolute_explorr_override(self):
        self.explorr.unlink()
        plugin = self.base / 'installed plugin'
        (plugin / 'bin').mkdir(parents=True)
        target = plugin / 'bin' / 'explorr'
        self.write_executable(target, FAKE_PROGRAM)
        self.env['HERDR_PLUGIN_ROOT'] = str(plugin)
        self.assertEqual(self.invoke(edit=True).returncode, 0)
        self.assertEqual(shlex.split(self.calls()[1][3])[1], str(target))
        override = self.base / 'absolute explorr'
        self.write_executable(override, FAKE_PROGRAM)
        self.env['EXPLORR_BIN_PATH'] = str(override)
        self.assertEqual(self.invoke(edit=True).returncode, 0)
        self.assertEqual(shlex.split(self.calls()[3][3])[1], str(override))

    def test_missing_explorr_fails_before_split(self):
        self.explorr.unlink()
        self.env['PATH'] = os.defpath
        result = self.invoke(edit=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('missing bundled explorr', result.stderr)
        self.assertEqual(self.calls(), [])

    def test_invalid_explorr_override_fails_before_split(self):
        self.env['EXPLORR_BIN_PATH'] = 'relative-explorr'
        self.assertNotEqual(self.invoke(edit=True).returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_helper_copied_directly_into_plugin_bin_uses_actual_sibling(self):
        plugin_bin = self.base / 'copied plugin' / 'bin'
        plugin_bin.mkdir(parents=True)
        shutil.copy2(ROOT / 'open-file', plugin_bin / 'herdr-review-last-markdown')
        self.write_executable(plugin_bin / 'herdr-reviewr', FAKE_PROGRAM)
        result = subprocess.run([str(plugin_bin / 'herdr-review-last-markdown'),
                                 str(self.file), '2', '1'], env=self.env,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(shlex.split(self.calls()[1][3])[1], str(plugin_bin / 'herdr-reviewr'))

    def test_herdr_path_fallback_uses_argv(self):
        del self.env['HERDR_BIN_PATH']
        shutil.copy2(self.manager, self.launch / 'herdr')
        self.env['PATH'] = str(self.launch) + os.pathsep + self.env['PATH']
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls()), 2)

    def test_invalid_missing_directory_or_control_path_fails_before_split(self):
        for file in [self.base / 'missing.md', self.base, self.base / 'newline\n.md']:
            with self.subTest(file=file):
                self.assertNotEqual(self.invoke(file=file).returncode, 0)
                self.assertEqual(self.calls(), [])

    def test_control_character_in_resolved_symlink_path_fails_before_split(self):
        target = self.base / 'control\t.md'
        target.write_text('file')
        alias = self.base / 'safe-alias.md'
        alias.symlink_to(target)
        self.assertNotEqual(self.invoke(file=alias).returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_c1_controls_and_invalid_utf8_fail_before_split(self):
        for name in ['control\x7f.md', 'control\x85.md', 'control\x9f.md', os.fsdecode(b'invalid\xff.md')]:
            with self.subTest(name=repr(name)):
                file = self.base / name
                file.write_text('source')
                self.assertNotEqual(self.invoke(file=file).returncode, 0)
                self.assertEqual(self.calls(), [])
                alias = self.base / 'valid-alias.md'
                alias.symlink_to(file)
                self.assertNotEqual(self.invoke(file=alias).returncode, 0)
                self.assertEqual(self.calls(), [])
                alias.unlink()

    def test_coordinate_validation_and_u32_limit_precede_split(self):
        for line, col in [('0', '1'), ('1', '0'), ('-1', '1'), ('1;touch bad', '1'),
                          ('4294967296', '1'), ('1', str(2**63)), ('1', '1\n')]:
            with self.subTest(line=line, col=col):
                self.assertNotEqual(self.invoke(line=line, col=col).returncode, 0)
                self.assertEqual(self.calls(), [])
        self.assertEqual(self.invoke(line='4294967295').returncode, 0)
        self.assertEqual(shlex.split(self.calls()[1][3])[-1], '4294967295')


if __name__ == '__main__':
    unittest.main()
