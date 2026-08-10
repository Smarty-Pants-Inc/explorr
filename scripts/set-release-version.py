#!/usr/bin/env python3
"""Check or update Explorr's binary and HerdR plugin versions together."""

from __future__ import annotations

import re
import sys
import tempfile
from pathlib import Path

SEMVER = re.compile(r"\d+\.\d+\.\d+")
GO_VERSION = re.compile(r'(?m)^const Version = "(\d+\.\d+\.\d+)"$')
MANIFEST_VERSION = re.compile(r'(?m)^version = "(\d+\.\d+\.\d+)"$')
ROOT = Path(__file__).resolve().parents[1]
VERSION_FILE = ROOT / "internal/version/version.go"
MANIFEST_FILE = ROOT / "herdr/herdr-plugin.toml"


def extract(path: Path, pattern: re.Pattern[str]) -> str:
    matches = pattern.findall(path.read_text())
    if len(matches) != 1:
        raise ValueError(f"expected one version in {path}, found {len(matches)}")
    return matches[0]


def check(version_file: Path, manifest_file: Path) -> str:
    binary_version = extract(version_file, GO_VERSION)
    plugin_version = extract(manifest_file, MANIFEST_VERSION)
    if binary_version != plugin_version:
        raise ValueError(
            f"version mismatch: binary {binary_version}, HerdR plugin {plugin_version}"
        )
    return binary_version


def update(path: Path, pattern: re.Pattern[str], version: str) -> None:
    content, count = pattern.subn(
        lambda match: match.group(0).replace(match.group(1), version),
        path.read_text(),
    )
    if count != 1:
        raise ValueError(f"expected one version in {path}, found {count}")
    path.write_text(content)


def set_version(version: str, version_file: Path, manifest_file: Path) -> None:
    if not SEMVER.fullmatch(version):
        raise ValueError(f"invalid version: {version}")
    update(version_file, GO_VERSION, version)
    update(manifest_file, MANIFEST_VERSION, version)
    check(version_file, manifest_file)


def self_test() -> None:
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        version_file = root / "version.go"
        manifest_file = root / "herdr-plugin.toml"
        version_file.write_text('package version\n\nconst Version = "1.0.0"\n')
        manifest_file.write_text('id = "com.smartypants.explorr"\nversion = "1.0.0"\n')
        assert check(version_file, manifest_file) == "1.0.0"
        set_version("1.2.3", version_file, manifest_file)
        assert check(version_file, manifest_file) == "1.2.3"
        manifest_file.write_text('version = "1.2.4"\n')
        try:
            check(version_file, manifest_file)
        except ValueError:
            pass
        else:
            raise AssertionError("mismatched versions were accepted")


def main() -> int:
    args = sys.argv[1:]
    try:
        if args == ["--self-test"]:
            self_test()
            return 0
        if args == ["--check"]:
            print(check(VERSION_FILE, MANIFEST_FILE))
            return 0
        if len(args) == 1 and not args[0].startswith("-"):
            set_version(args[0], VERSION_FILE, MANIFEST_FILE)
            print(args[0])
            return 0
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        return 1

    print(
        f"usage: {Path(sys.argv[0]).name} --check | --self-test | VERSION",
        file=sys.stderr,
    )
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
