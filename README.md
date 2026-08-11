# Explorr

Explorr is a mouse-friendly terminal editor and file explorer for
[Smarty HerdR](https://github.com/Smarty-Pants-Inc/herdr). It is a single Go binary with project
search, LSP diagnostics and navigation, Git views, and DAP debugging.

Explorr is derived from [vonzelle-vzt/herdr-edit](https://github.com/vonzelle-vzt/herdr-edit) and
[cloudmanic/spice-edit](https://github.com/cloudmanic/spice-edit). See [FORK.md](FORK.md) for the
fork history and implementation details.

## Install

### Smarty HerdR

Requires Smarty HerdR 0.8.0 or newer and `jq`:

```sh
herdr plugin install Smarty-Pants-Inc/explorr/herdr --yes
```

With a global install, the bundled lifecycle requires `explorr` and `jq` on `PATH`: run
`explorr herdr install`, `explorr herdr check`, or `explorr herdr remove`.

In Smarty HerdR, `prefix+f` opens the workspace explorer and `prefix+shift+f` opens a full editor
tab.

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
