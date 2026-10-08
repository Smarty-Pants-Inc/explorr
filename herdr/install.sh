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
# Linking disabled in a private registry validates the real split/link-handler
# schema without contacting the running session or executing plugin hooks.
probe_root="$(mktemp -d "${TMPDIR:-/tmp}/explorr-herdr-capabilities-XXXXXX")"
trap 'rm -rf "$probe_root"' EXIT
trap 'exit 1' HUP INT TERM
probe_herdr() (
	unset HERDR_SESSION HERDR_PANE_ID HERDR_TAB_ID HERDR_WORKSPACE_ID HERDR_CLIENT_SOCKET_PATH
	HOME="$probe_root/home"
	XDG_CONFIG_HOME="$probe_root/config"
	XDG_DATA_HOME="$probe_root/data"
	XDG_STATE_HOME="$probe_root/state"
	XDG_CACHE_HOME="$probe_root/cache"
	XDG_RUNTIME_DIR="$probe_root/runtime"
	HERDR_CONFIG_PATH="$probe_root/config/herdr/config.toml"
	HERDR_SOCKET_PATH="$probe_root/offline.sock"
	export HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_CACHE_HOME XDG_RUNTIME_DIR
	export HERDR_CONFIG_PATH HERDR_SOCKET_PATH
	"$herdr_bin" "$@"
)
required="explorr: Smarty HerdR 0.9.1+ with targeted split plugin panes and local file-link handlers is required"
if ! pane_help="$(probe_herdr plugin pane open --help 2>&1)"; then
	printf '%s\n' "$required" "$pane_help" >&2
	exit 1
fi
for flag in --placement --target-pane --direction; do
	if ! printf '%s\n' "$pane_help" | grep -Eq "(^|[[:space:]])$flag([[:space:]=,]|$)"; then
		printf '%s\n' "$required (missing plugin pane open $flag)" >&2
		exit 1
	fi
done
if ! probe_herdr plugin link "$plugin_root" --disabled >/dev/null; then
	printf '%s\n' "$required" >&2
	exit 1
fi
if ! registry="$(probe_herdr plugin list --plugin com.smartypants.explorr --json)"; then
	printf '%s\n' "$required" >&2
	exit 1
fi
if ! printf '%s\n' "$registry" | jq -e 'any(.result.plugins[]?;
	.plugin_id == "com.smartypants.explorr" and
	any(.panes[]?; .id == "explorer" and .placement == "split") and
	any(.link_handlers[]?; .id == "local-file" and .pattern == "^file://" and .action == "open-file"))' >/dev/null; then
	printf '%s\n' "$required" >&2
	exit 1
fi

mkdir -p "$plugin_root/bin"
host_os="$(GOENV=off go env GOHOSTOS)"
host_arch="$(GOENV=off go env GOHOSTARCH)"
GOENV=off GOOS="$host_os" GOARCH="$host_arch" \
	GO386= GOAMD64= GOARM= GOARM64= GOMIPS= GOMIPS64= GOPPC64= GORISCV64= GOWASM= GOLOONG64= \
	go build -o "$plugin_root/bin/explorr" "$repo_root"
