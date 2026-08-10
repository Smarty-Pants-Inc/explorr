#!/usr/bin/env python3
"""Render Explorr's Homebrew formula from GoReleaser checksums."""

from __future__ import annotations

import re
import sys
import tempfile
from pathlib import Path

TARGETS = (
    ("darwin", "arm64"),
    ("darwin", "amd64"),
    ("linux", "arm64"),
    ("linux", "amd64"),
)


def load_checksums(version: str, path: Path) -> dict[tuple[str, str], str]:
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError(f"invalid version: {version}")

    wanted = {
        f"explorr_{version}_{os_name}_{arch}.tar.gz": (os_name, arch)
        for os_name, arch in TARGETS
    }
    checksums: dict[tuple[str, str], str] = {}
    for line in path.read_text().splitlines():
        fields = line.split()
        if len(fields) != 2 or fields[1] not in wanted:
            continue
        checksum, filename = fields
        if not re.fullmatch(r"[0-9a-fA-F]{64}", checksum):
            raise ValueError(f"invalid SHA-256 for {filename}")
        checksums[wanted[filename]] = checksum.lower()

    missing = [f"{os_name}/{arch}" for os_name, arch in TARGETS if (os_name, arch) not in checksums]
    if missing:
        raise ValueError(f"missing checksums: {', '.join(missing)}")
    return checksums


def render(version: str, checksums: dict[tuple[str, str], str]) -> str:
    def artifact(os_name: str, arch: str) -> str:
        filename = f"explorr_{version}_{os_name}_{arch}.tar.gz"
        return (
            f'      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v{version}/{filename}"\n'
            f'      sha256 "{checksums[(os_name, arch)]}"'
        )

    return f'''# typed: false
# frozen_string_literal: true

class Explorr < Formula
  desc "Mouse-first terminal code editor with LSP diagnostics"
  homepage "https://github.com/Smarty-Pants-Inc/explorr"
  version "{version}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
{artifact("darwin", "arm64")}
    else
{artifact("darwin", "amd64")}
    end
  end

  on_linux do
    if Hardware::CPU.arm?
{artifact("linux", "arm64")}
    else
{artifact("linux", "amd64")}
    end
  end

  def install
    bin.install "explorr"
  end

  test do
    assert_match "explorr #{{version}}", shell_output("#{{bin}}/explorr --version")
  end
end
'''


def self_test() -> None:
    with tempfile.TemporaryDirectory() as directory:
        checksums_path = Path(directory) / "checksums.txt"
        checksums_path.write_text(
            "\n".join(
                f"{index + 1:064x}  explorr_1.0.0_{os_name}_{arch}.tar.gz"
                for index, (os_name, arch) in enumerate(TARGETS)
            )
        )
        formula = render("1.0.0", load_checksums("1.0.0", checksums_path))
        assert 'version "1.0.0"' in formula
        assert all(f"explorr_1.0.0_{os_name}_{arch}.tar.gz" in formula for os_name, arch in TARGETS)


def main() -> int:
    if sys.argv[1:] == ["--self-test"]:
        self_test()
        return 0
    if len(sys.argv) != 4:
        print(f"usage: {Path(sys.argv[0]).name} VERSION CHECKSUMS OUTPUT", file=sys.stderr)
        return 2

    version, checksums_path, output_path = sys.argv[1:]
    try:
        formula = render(version, load_checksums(version, Path(checksums_path)))
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        return 1

    Path(output_path).write_text(formula)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
