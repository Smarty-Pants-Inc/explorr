#!/usr/bin/env bash
# Install local guards that stop Explorr pushing into either upstream repository.
#
# Explorr derives from vonzelle-vzt/herdr-edit, itself based on cloudmanic/spice-edit.
# GitHub permissions are not a safety mechanism: a release pipeline once tried to write a formula
# into an upstream repository, and only a 403 prevented it.
#
# Three layers, because they fail differently:
#   1. `remote.upstream.pushurl = DISABLED`  — stops `git push upstream`. Cheap, but only covers
#      the remote by name, and a `git remote set-url` undoes it silently.
#   2. a pre-push hook                       — refuses any push whose URL mentions either upstream
#      repo, however it was spelled. Survives (1) being undone, and covers a script that builds the
#      URL itself. Independent of whether you happen to lack write access.
#   3. `remote.origin.gh-resolved = base`    — pins `gh`'s default repo to Explorr.
#
# Layer 3 exists because layers 1 and 2 are both git hooks, and `gh pr create` performs no git
# operation at all. In a forked repository, bare `gh pr create` can default its base to a parent.
# Setting gh's default repo is the only guard for that path.
#
# Hooks live in .git/ and are therefore NOT cloned. Re-run this after a fresh clone.
# Idempotent: safe to run repeatedly.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT" || exit 1

UPSTREAM_REPOS=("vonzelle-vzt/herdr-edit" "cloudmanic/spice-edit")
is_upstream_url() {
  local url="$1" repo
  for repo in "${UPSTREAM_REPOS[@]}"; do
    [[ "$url" == *"$repo"* ]] && return 0
  done
  return 1
}

# --- layer 1 -----------------------------------------------------------------------------------
if git remote | grep -qx upstream; then
  git remote set-url --push upstream DISABLED
  echo "  upstream push url  -> DISABLED"
else
  echo "  no 'upstream' remote; skipping push-url guard"
fi

# --- layer 2 -----------------------------------------------------------------------------------
HOOK=".git/hooks/pre-push"
mkdir -p .git/hooks
cat > "$HOOK" <<EOF
#!/usr/bin/env bash
# Refuse to push anything into an upstream repository this project derives from.
# Installed by scripts/install-guards.sh — see that file for why.
set -uo pipefail
remote_url="\${2:-}"
case "\$remote_url" in
  *vonzelle-vzt/herdr-edit*|*cloudmanic/spice-edit*)
    echo "pre-push: refusing to push to \$remote_url" >&2
    echo "  That is an upstream repository, not Explorr." >&2
    echo "  Contribute there by opening a pull request instead." >&2
    echo "  Override deliberately with --no-verify if you really mean it." >&2
    exit 1
    ;;
esac
exit 0
EOF
chmod +x "$HOOK"
echo "  pre-push hook      -> installed ($HOOK)"

# --- layer 3 -----------------------------------------------------------------------------------
# "base" is gh's spelling for "the repo this remote points at IS the default", which is what we
# want: origin is the fork. Written with git config rather than `gh repo set-default` so the guard
# installs identically on a machine with no gh, or with gh unauthenticated.
if git remote | grep -qx origin; then
  origin_url="$(git remote get-url origin 2>/dev/null || echo)"
  if is_upstream_url "$origin_url"; then
    echo "  gh default repo    -> REFUSED: origin points at upstream ($origin_url)" >&2
  else
    git config --local remote.origin.gh-resolved base
    echo "  gh default repo    -> origin ($origin_url)"
  fi
else
  echo "  no 'origin' remote; skipping gh default-repo guard"
fi

echo
echo "  git push --dry-run https://github.com/vonzelle-vzt/herdr-edit main   # must be refused"
echo "  git push --dry-run https://github.com/cloudmanic/spice-edit main     # must be refused"
echo "  gh repo set-default --view                                            # must say Smarty-Pants-Inc/explorr"
