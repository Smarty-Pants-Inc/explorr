#!/bin/sh
# Copyright (c) 2026 Smarty Pants, Inc. SPDX-License-Identifier: MIT
# Build our checksum-pinned downstream Reviewr. Never register/activate a plugin.
set -eu

fatal() {
	printf 'reviewr: %s\n' "$*" >&2
	exit 1
}

for cmd in python3 curl tar patch cargo rustc; do
	command -v "$cmd" >/dev/null 2>&1 || fatal "$cmd is required (Rust >= 1.97)"
done
# realpath handles an installer invoked through a bundle symlink on Linux/macOS.
root="$(python3 -c 'import os,sys; print(os.path.dirname(os.path.realpath(sys.argv[1])))' "$0")"
[ -s "$root/upstream.patch" ] || fatal "missing $root/upstream.patch (assemble the downstream patch first)"
[ -x "$root/open-file" ] || fatal "missing executable $root/open-file"
[ -x "$root/edit-original" ] || fatal "missing executable $root/edit-original"
python3 - <<'PY'
import re, subprocess, sys
version = subprocess.check_output(['rustc', '--version'], text=True)
match = re.match(r'rustc (\d+)\.(\d+)\.(\d+)', version)
if not match or tuple(map(int, match.groups())) < (1, 97, 0):
    sys.exit('reviewr: Rust >= 1.97 is required')
PY

# Pin is data, not evaluated shell source. Validate before filesystem/network use.
python3 - "$root/upstream.json" <<'PY'
import json, re, sys
pin = json.load(open(sys.argv[1]))
checks = [
    (pin['archive_url'].startswith('https://codeload.github.com/'), 'invalid archive URL'),
    (re.fullmatch('[a-f0-9]{64}', pin['sha256']), 'invalid archive checksum'),
    (re.fullmatch('[A-Za-z0-9][A-Za-z0-9._-]*', pin['archive_root']), 'invalid archive root'),
    (pin['binary'] == 'herdr-reviewr', 'unexpected binary'),
    (pin['patch'] == 'upstream.patch', 'unexpected patch'),
]
for valid, message in checks:
    if not valid:
        sys.exit('reviewr: ' + message)
PY
url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["archive_url"])' "$root/upstream.json")"
archive_root="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["archive_root"])' "$root/upstream.json")"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/explorr-reviewr-build.XXXXXX")"
staged=
trap 'rm -rf "$tmp"; if [ -n "$staged" ]; then rm -f "$staged"; fi' 0
trap 'exit 1' HUP INT TERM
printf 'reviewr: downloading pinned v0.30.1 source\n' >&2
curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' \
	--connect-timeout 20 --max-time 180 --retry 2 --output "$tmp/source.tar.gz" "$url"
python3 - "$root/upstream.json" "$tmp/source.tar.gz" <<'PY'
import hashlib, json, sys
expected = json.load(open(sys.argv[1]))['sha256']
digest = hashlib.sha256()
with open(sys.argv[2], 'rb') as archive:
    for chunk in iter(lambda: archive.read(1024 * 1024), b''):
        digest.update(chunk)
actual = digest.hexdigest()
if actual != expected:
    sys.exit(f'reviewr: checksum mismatch (expected {expected}, got {actual})')
PY
# The checksum must pass before either extraction or patch application.
tar -xzf "$tmp/source.tar.gz" -C "$tmp"
[ -f "$tmp/$archive_root/Cargo.lock" ] || fatal "pinned archive is missing Cargo.lock"
(
	cd "$tmp/$archive_root"
	patch --batch --forward -p1 < "$root/upstream.patch"
	cargo build --locked --release --target-dir "$tmp/target"
)
[ -x "$tmp/target/release/herdr-reviewr" ] || fatal "build did not produce herdr-reviewr"
mkdir -p "$root/bin"
staged="$(mktemp "$root/bin/.herdr-reviewr.XXXXXX")"
cp "$tmp/target/release/herdr-reviewr" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$root/bin/herdr-reviewr"
staged=
ln -sfn ../open-file "$root/bin/herdr-review-last-markdown"
ln -sfn ../edit-original "$root/bin/herdr-review-edit-original"
printf 'reviewr: staged %s/bin/herdr-reviewr and launch helpers (no plugin activation)\n' "$root" >&2
