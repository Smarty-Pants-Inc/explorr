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
# Managed builds intentionally omit runtime plugin context. HerdR supplies the
# executable that launched this build separately, so probe that exact manager
# instead of an unrelated `herdr` found on PATH. Direct script runs fall back to
# PATH because they have no manager-provided executable.
if [ -n "${HERDR_BUILD_BIN_PATH:-}" ]; then
	case "$HERDR_BUILD_BIN_PATH" in
	/*) herdr_bin=$HERDR_BUILD_BIN_PATH ;;
	*)
		printf '%s\n' "explorr: HERDR_BUILD_BIN_PATH must be an absolute executable path" >&2
		exit 1
		;;
	esac
else
	herdr_bin=herdr
fi
if ! "$herdr_bin" pane split --help 2>&1 | grep -Eq '(^|[[:space:]])--workspace([[:space:]=,]|$)'; then
	printf '%s\n' "explorr: Smarty HerdR with pane split --workspace is required" >&2
	exit 1
fi

mkdir -p "$plugin_root/bin"
host_os="$(GOENV=off go env GOHOSTOS)"
host_arch="$(GOENV=off go env GOHOSTARCH)"
GOENV=off GOOS="$host_os" GOARCH="$host_arch" \
	GO386= GOAMD64= GOARM= GOARM64= GOMIPS= GOMIPS64= GOPPC64= GORISCV64= GOWASM= GOLOONG64= \
	go build -o "$plugin_root/bin/explorr" "$repo_root"
