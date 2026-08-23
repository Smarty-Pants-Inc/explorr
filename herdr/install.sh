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

command -v jq >/dev/null 2>&1 || {
	printf '%s\n' "explorr: jq is required but not found on PATH" >&2
	exit 1
}
herdr_bin=${HERDR_BIN_PATH:-herdr}
if ! "$herdr_bin" pane split --help 2>&1 | grep -Eq '(^|[[:space:]])--workspace([[:space:]=,]|$)'; then
	printf '%s\n' "explorr: Smarty HerdR with pane split --workspace is required" >&2
	exit 1
fi


mkdir -p "$plugin_root/bin"
GOOS= GOARCH= go build -o "$plugin_root/bin/explorr" "$repo_root"
