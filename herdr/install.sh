#!/bin/sh
set -eu

case "$0" in
*/*) plugin_root=${0%/*} ;;
*) plugin_root=. ;;
esac
plugin_root="$(CDPATH= cd "$plugin_root" && pwd)"
repo_root="$(CDPATH= cd "$plugin_root/.." && pwd)"

command -v go >/dev/null 2>&1 || {
	printf '%s\n' "explorr: Go is required but not found on PATH" >&2
	exit 1
}

mkdir -p "$plugin_root/bin"
go build -o "$plugin_root/bin/explorr" "$repo_root"
