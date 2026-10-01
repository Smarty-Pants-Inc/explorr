# Explorr

Explorr is a mouse-friendly terminal editor and file explorer for
[Smarty HerdR](https://github.com/Smarty-Pants-Inc/herdr). It is a single Go binary with project
search, LSP diagnostics and navigation, Git views, and DAP debugging.

Explorr is derived from [vonzelle-vzt/herdr-edit](https://github.com/vonzelle-vzt/herdr-edit) and
[cloudmanic/spice-edit](https://github.com/cloudmanic/spice-edit). See [FORK.md](FORK.md) for the
fork history and implementation details.

## Install

### Smarty HerdR

Requires Smarty HerdR **0.9.1 or newer** and `jq`. A managed source install also requires Go
on `PATH`.

Before either installation method, if the existing `smarty.file-links` plugin is installed,
disable it so the two plugins do not compete for the same `^file://` links:

```sh
herdr plugin disable smarty.file-links
```

For a managed source install:

```sh
herdr plugin install Smarty-Pants-Inc/explorr/herdr --yes
```

#### Linux release artifact and local link (no Go required)

Download the matching binary and bundled manifest from the same release, verify its checksum,
and link the extracted plugin locally. Run these commands in the HerdR session you intend to
configure, after resolving any file-link handler collision above:

```sh
version="$(curl -fsSL https://api.github.com/repos/Smarty-Pants-Inc/explorr/releases/latest | jq -er '.tag_name | ltrimstr("v")')" || exit 1
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo 'Unsupported Linux architecture' >&2; exit 1 ;;
esac
archive="explorr_${version}_linux_${arch}.tar.gz"
release="https://github.com/Smarty-Pants-Inc/explorr/releases/download/v${version}"
dir="$HOME/.local/share/explorr/releases/$version"
mkdir -p "$dir"
(
  set -eu
  cd "$dir"
  curl -fLO "$release/$archive"
  curl -fLO "$release/checksums.txt"
  awk -v file="$archive" '$2 == file { print }' checksums.txt | sha256sum -c - || exit 1
  tar -xzf "$archive"
  mkdir -p herdr/bin
  ln -sf ../../explorr herdr/bin/explorr
) || exit 1
herdr plugin link "$dir/herdr"
```

The release must include the HerdR 0.9.1-compatible manifest (`min_herdr_version = "0.9.1"`
and explorer `placement = "split"`). A local link does not run the source-build script; the
`herdr/bin/explorr` link makes actions execute the extracted release binary.

With a global install, the bundled lifecycle requires `explorr` and `jq` on `PATH`: run
`explorr herdr install`, `explorr herdr check`, or `explorr herdr remove`. Managed and
release-local installs prefer the bundled binary; the global lifecycle falls back to the
version-checked binary on `PATH`. HerdR 0.9.1 supports offline linking/checking; removal with
`explorr herdr remove` requires a running HerdR server.

Local file links open a visible, **unfocused** editor split to the right of the originating
pane (`HERDR_PANE_ID`), not whichever pane later happens to have focus. Click the new split to
focus it. Markdown links and links marked `?review=1` use the pinned, patched Reviewr integration
in [`reviewr/`](reviewr/README.md). A missing or failed Reviewr helper is an error, not an
Explorr fallback: review comments must not be lost. Other files open in Explorr. Links accept
`file://<local-host>/absolute/path#42` or explicit `?line=42&col=2` positions; encode the path
as a URL. Dedicated link editors do not consume or publish another editor's global open/debug
panel state. Their text saves bind the original file and directory: a replaced file or parent
is refused without discarding your edits. Automatic path-based format-on-save is disabled in
these dedicated panes; ordinary project editors retain it. Linking the plugin or creating a
workspace does **not** automatically create explorer sidebars.

To explicitly open the explorer beside your current HerdR pane:

```sh
herdr plugin pane open --plugin com.smartypants.explorr --entrypoint explorer \
  --placement split --target-pane "$HERDR_PANE_ID" --direction right --cwd "$PWD" --no-focus
```

The explorer entrypoint remains available through HerdR's plugin pane controls. In Smarty
HerdR configurations that bind them, `prefix+f` opens the explorer and `prefix+shift+f` opens
a full editor tab.

### Standalone

Homebrew on macOS or Linux:

```sh
brew tap Smarty-Pants-Inc/explorr https://github.com/Smarty-Pants-Inc/explorr
brew install Smarty-Pants-Inc/explorr/explorr
```

Or use the checksum-verifying installer:

```sh
curl -fsSL https://raw.githubusercontent.com/Smarty-Pants-Inc/explorr/main/install.sh | sh
```

Prebuilt binaries are available from [GitHub Releases](https://github.com/Smarty-Pants-Inc/explorr/releases/latest).

## Use

```sh
explorr [path]
explorr --help
```

`Esc` is the leader key. Press `Esc` twice to open the action menu. Mouse input works for cursor
placement, selection, scrolling, the file tree, and dialogs.

## Build and test

```sh
git clone https://github.com/Smarty-Pants-Inc/explorr
cd explorr
go build -o explorr .
make test
```

## License

MIT. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

Copyright © 2026 Cloudmanic, LLC. (upstream work)
Copyright © 2026 Vonzelle Brown (fork modifications)
