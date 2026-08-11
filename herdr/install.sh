#!/bin/sh
set -eu

plugin_root="$(CDPATH= cd "$(dirname "$0")" && pwd)"
repo_root="$(dirname "$plugin_root")"

command -v jq >/dev/null 2>&1 || {
	printf '%s\n' "explorr: jq is required but not found on PATH" >&2
	exit 1
}

version="$(awk -F '"' '/^version = "/ { print $2; exit }' "$plugin_root/herdr-plugin.toml")"
[ -n "$version" ] || {
	printf '%s\n' "explorr: could not read the plugin version" >&2
	exit 1
}

install_dir="${INSTALL_DIR:-$plugin_root/bin}"
SKIP_PATH_WARNING=1 VERSION="v$version" INSTALL_DIR="$install_dir" sh "$repo_root/install.sh"
