# Patched Reviewr for Explorr file links

This is a small downstream integration of **herdr-reviewr v0.30.1**, not a new
HerdR plugin. The existing Explorr local-file link handler invokes
`herdr-review-last-markdown FILE LINE COL` for Markdown. The helper opens the
patched viewer in a right split beside the **originating pane**. Document mode
reviews exactly that file, including files outside Git repositories.

The upstream source is downloaded, not vendored here. `upstream.json` pins its
archive URL and SHA-256; `NOTICE` records provenance and `LICENSE` preserves the
upstream MIT grant. `upstream.patch` contains the downstream source changes.

## Build

Dependencies: **Rust/Cargo >= 1.97**, a native linker/C build toolchain for the
Rust dependencies, Python **3.8+** (standard library only), curl, tar, and patch.
Runtime needs the patched binary, Python, Smarty HerdR with explicit pane
split/run support (0.9.1+), and Explorr for editing originals. No jq, Python
packages, upstream Reviewr plugin, or forge authentication is needed by these
helpers. Git is not required to view one document; ordinary upstream worktree
review still needs it.

From the Explorr checkout:

```sh
# Put your chosen >=1.97 toolchain on PATH first, if necessary.
sh reviewr/install.sh
python3 -m unittest discover -s reviewr -p '*_test.py' -v
```

The installer creates its **own mktemp directory**, downloads the exact pinned
archive, verifies its checksum **before extraction**, applies
`reviewr/upstream.patch` using `patch -p1`, and runs
`cargo build --locked --release --target-dir "$TMP_BUILD/target"`. The explicit
private target directory overrides any external `CARGO_TARGET_DIR`; the staged
artifact is read from that same private directory. It stages:

```text
reviewr/bin/herdr-reviewr
reviewr/bin/herdr-review-last-markdown -> ../open-file
reviewr/bin/herdr-review-edit-original -> ../edit-original
reviewr/edit-original -> open-file
```

Temporary source/build files are removed on success or failure. An unsuccessful
checksum, patch, or build does not overwrite an existing staged binary. The
installer refuses a missing patch/helper or an old Rust toolchain before
network use. It neither links nor activates any HerdR plugin. Cargo may fetch
locked dependencies; the archive pin is not a claim of fully vendored or
bit-for-bit reproducible builds.

## Launch contracts and focus

```sh
herdr-review-last-markdown FILE LINE COL
herdr-review-edit-original FILE LINE COL
```

- `HERDR_PANE_ID` is mandatory and must be a real HerdR handle such as `w1:p1`,
  not `current`, `focused`, a workspace, or a pseudo pane. HerdR itself must
  accept the handle; a stale origin **fails with no fallback**. Ambient
  workspace/tab/focus hints are not consulted.
- `HERDR_BIN_PATH`, when nonempty, selects the exact manager executable;
  otherwise the helper uses `herdr` from PATH.
- `FILE` must exist as a regular file. The helper resolves aliases/symlinks to
  an absolute canonical path and uses its parent as cwd. Quotes, spaces,
  colons, leading dashes, and shell metacharacters are passed safely. Control
  characters (C0, DEL, and C1, including newline, tab, and NUL) and invalid UTF-8
  in either the supplied or canonical path are refused before creating a pane.
- Coordinates are positive, 1-based decimal integers. Reviewr line numbers must
  fit `u32` (`1..4294967295`). Editing coordinates must fit the platform integer.
  **COL is accepted/validated but ignored by Reviewr:** its document anchor is
  a line, not a character cursor. Native Explorr editing retains both LINE and
  COL. The applications clamp a valid coordinate to available content.
- The helper resolves its own file through symlinks, preferring
  `reviewr/bin/herdr-reviewr`, or an actual sibling binary if copied directly
  into a plugin's `bin/`. It **never substitutes an upstream Reviewr on PATH**.
  Missing/nonexecutable dependencies fail before a split is requested.
- Edit-original finds bundled Explorr, including a sibling `herdr/bin/explorr`
  or `$HERDR_PLUGIN_ROOT/bin/explorr`, then PATH. An explicit
  `EXPLORR_BIN_PATH` overrides this and must be an absolute executable path;
  an invalid override is an error, not a fallback.

Both helpers use argv subprocess calls for:

```text
herdr pane split --pane ORIGIN --direction right --cwd PARENT --no-focus
herdr pane run NEW_PANE "exec ...safely shell-quoted command..."
```

The first helper executes `herdr-reviewr --file ABS --line N`. The second
executes **`explorr --single-file-at ABS LINE COL`**, never `--herdr-open` or
`--open-at`. Thus editing Markdown does not recursively reopen Reviewr or send
an open request to an unrelated global editor. A rejected `pane run` causes one
best-effort `pane close NEW_PANE`; cleanup errors are also reported. The
helper never closes the source pane or retries using a global target.

There is **no automatic startup pane**, worktree hook, or new Reviewr plugin.
The split is visible but unfocused. These CLI helpers have no originating-client
focus token, so they deliberately make **no client/session focus call**. Click
the new pane to interact with it. Installing/building alone opens nothing.

## Edit the source, not a comment

In patched document mode, the visible **Edit original** footer action (also
uppercase `E`) launches the `herdr-review-edit-original` helper beside the installed
Reviewr binary with the canonical source file, selected current source line, and column `1`.
This does not depend on the new pane inheriting the link action's PATH. Lowercase `e` still edits a
review comment. The source opens as a native, single-file Explorr editor in
another explicit, unfocused right split. Reviewr remains the read/review pane;
it does not write the original. Removed historical lines are not valid current
source edit targets. A failed launch is shown as an error, not reported as a
successful edit.

## Durable comments and agent reading

**Document mode** appends `.reviewr.json` to the entire canonical source name:
`/work/notes.md` has `/work/notes.md.reviewr.json`. This is a JSON snapshot beside
the original, not a log, clipboard, or terminal scrape. Successful comment
add/edit/delete updates it atomically before acknowledgment. A failed save
preserves the previous snapshot and reports an error. Reopening document mode
restores saved comments. Copy/Send does **not** consume document comments;
delete is the explicit removal action. Unsubmitted composer text is not a
saved comment. The original source remains unchanged.

The exact schema is:

| Field | Meaning |
| --- | --- |
| `schema_version` | Integer `1` |
| `document` | Absolute canonical source path |
| `comments` | Array of the seven-field comment records below |
| `comments[].file` | Same absolute canonical source path |
| `comments[].side` | Lowercase `"new"` or `"old"`; document comments use `"new"` |
| `comments[].start`, `comments[].end` | Inclusive, 1-based line range (`u32`) |
| `comments[].lines` | Verbatim anchored snippet, including line markers |
| `comments[].text` | Reviewer comment text |
| `comments[].diff_anchored` | Boolean; `false` for document content comments |

There are **no stable IDs, timestamps, author, delivery receipts, or schema
extensions** in this contract. It is the current comment set, not a revision
history; anchors/snippets are not silently rebased after source edits. Agents
should read the snapshot explicitly and interpret the snippet against current
content. Sidecars can contain sensitive source/reviewer text; do not publish or
commit them automatically.

A safe agent read command accepts the original filename as an argument and
resolves aliases exactly as the viewer does:

```sh
python3 - /absolute/path/to/notes.md <<'PY'
import json, pathlib, sys
original = pathlib.Path(sys.argv[1]).resolve(strict=True)
sidecar = pathlib.Path(str(original) + '.reviewr.json')
with sidecar.open() as stream:
    snapshot = json.load(stream)
if snapshot['schema_version'] != 1 or snapshot['document'] != str(original):
    raise SystemExit('unexpected document snapshot')
print(json.dumps(snapshot, ensure_ascii=False, indent=2))
PY
```

A missing sidecar means no saved snapshot yet. Malformed, unsupported,
foreign-document, or symlink sidecars are refused by the viewer rather than
repaired/overwritten. A persistent `.reviewr.json.lock` file holds an advisory
single-writer lock while the viewer is alive; exit releases the lock without
removing the inode. Do not delete an active lock to bypass the one-writer rule.
Atomic replacement allows agents to read complete snapshots without that lock.
Persistence here is specific to `--file` document mode, not a promise that all
upstream Git-review sessions are durable. Send remains optional notification,
not the durable store or proof an agent submitted a message.

## Bundle/install recipe (explicit operator action)

Build Explorr and the patched viewer first. Copy the existing Explorr plugin
and this bundle together, preserving this layout and licenses. For example:

```sh
sh herdr/install.sh
sh reviewr/install.sh
DEST="${XDG_DATA_HOME:-$HOME/.local/share}/explorr"
mkdir -p "$DEST/herdr/bin" "$DEST/reviewr/bin"
cp herdr/herdr-plugin.toml "$DEST/herdr/"
cp herdr/bin/explorr "$DEST/herdr/bin/"
for name in open-file install.sh upstream.json upstream.patch LICENSE NOTICE README.md; do
    cp "reviewr/$name" "$DEST/reviewr/$name"
done
ln -sfn open-file "$DEST/reviewr/edit-original"
cp reviewr/bin/herdr-reviewr "$DEST/reviewr/bin/"
ln -sfn ../open-file "$DEST/reviewr/bin/herdr-review-last-markdown"
ln -sfn ../edit-original "$DEST/reviewr/bin/herdr-review-edit-original"
# Explorr resolves the review helper from HERDR_PLUGIN_ROOT/bin; bridge it there.
ln -sfn ../../reviewr/open-file "$DEST/herdr/bin/herdr-review-last-markdown"
ln -sfn ../../reviewr/edit-original "$DEST/herdr/bin/herdr-review-edit-original"
ln -sfn ../../reviewr/bin/herdr-reviewr "$DEST/herdr/bin/herdr-reviewr"
# Link only the existing Explorr plugin, when intentionally installing it:
herdr plugin link "$DEST/herdr"
```

HerdR supplies `HERDR_PLUGIN_ROOT` but does **not** prepend the plugin's `bin/` to
PATH. Explorr prefers the explicit `herdr/bin/` helper bridge above; Reviewr resolves
its edit helper beside its own executable. Preserve these symlinks (or copy the
helper and patched binary as actual siblings) so neither launch needs a fleet-wide
PATH change.
Do not link the upstream `persiyanov.reviewr` plugin: its startup hooks are not
part of this integration. The recipe is documentation; running the build
script does not run the plugin-link command.
