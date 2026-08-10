---
name: explorr-concurrent-session-collision
description: Explorr can be worked on by several simultaneous Claude sessions in one checkout; the branch can change mid-task and another session can sweep up uncommitted files.
metadata:
  type: project
---

Multiple Claude sessions once shared one Explorr working copy. Treat the git
state as shared and mutable whenever runtime evidence shows more than one session
using the same checkout.

Observed on 2026-07-31 while implementing Lane B stage 2 (the DAP client):

- The task began on `feat/lsp-status-and-live-oracle` (verified at start).
  Partway through, another session committed, merged that branch into `main`,
  checked `main` out and pulled. `git branch --show-current` then returned
  `main` — the instructed branch had changed under an in-flight task.
- My still-in-progress `internal/dap/*` files were swept into that session's
  `Release 0.13.0` commit. Nothing was lost, but they were committed by someone
  else, on a branch I was told not to be on, without review.
- Files written *after* that commit (`internal/app/debug.go`, `debug_test.go`)
  stayed untracked, so the working tree ended up split across two states.

**Why:** one checkout, many agents. `git add -A` / `git commit -am` in any pane
picks up every other pane's uncommitted work, and a `checkout` in any pane
rewrites the working tree everyone else is editing.

**How to apply:**
- Re-check `git branch --show-current` before any git operation late in a task,
  not just at the start. Do not assume the branch you verified is still current.
- Never `git add -A`, `git commit -am`, `git checkout`, `git reset` or
  `git stash` here — those are the operations that destroy or misappropriate
  another session's work. Stage explicit paths only.
- If the branch has moved or your files were committed by another session:
  **STOP and report it** rather than "fixing" it. Switching back or resetting
  can destroy the other session's in-flight work.
- Verify your own edits survived after any unexplained git state change — grep
  for a few of your *latest* edits specifically, since a `checkout` would have
  replaced your files with an older committed snapshot silently.
- Prefer a dedicated git worktree per session when the task is long.
