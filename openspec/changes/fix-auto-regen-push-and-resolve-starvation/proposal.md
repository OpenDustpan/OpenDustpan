---
version: 2
---

Short form: a bug in the CI workflows of this repository.

## Problem

**Persona: a contributor with a pull request in the merge queue.** The
contributor waits for the auto-regen workflows to regenerate the generated
files of the pull request after main moves. Two gaps stop that:

- **A. No patch is ever pushed.** `auto-regen-resolve` succeeds, but
  `auto-regen-push` never starts, so the generated files stay stale and the
  pull request stays dirty. The contributor sees no run and no comment.
- **B. A pull request can starve.** Each push to main starts a new `find`,
  which dispatches a new resolve run on the same branch. The concurrency
  group of the resolve workflow cancels the running one, so a busy main can
  cancel every resolve run of a pull request.

## Evidence

- **A: the push workflow has 0 runs, ever.**
  `gh api repos/OpenDustpan/OpenDustpan/actions/workflows/auto-regen-push.yml/runs --jq .total_count`
  answered `0` on 14 November 2026 at 18:20 UTC. The resolve workflow had 13
  runs. Two of them succeeded for #2554 at `62ce31ab` (runs 43623708678 and
  43624902909), and no patch reached the branch.
- **A: who starts the resolve runs.** The same API lists `actor` and
  `triggering_actor` `github-actions[bot]` for every resolve run, so the
  `GITHUB_TOKEN` started each of them. `.github/workflows/auto-regen.yml:105`
  passes `GH_TOKEN: ${{ github.token }}` to
  `./scripts/auto-regen.sh dispatch` at `:110`, and
  `scripts/auto-regen.sh:446` posts to
  `actions/workflows/auto-regen-resolve.yml/dispatches` with that token.
- **A: how the push workflow starts.**
  `.github/workflows/auto-regen-push.yml:12-13` starts it with
  `workflow_run` of `workflows: [ auto-regen-resolve ]`. GitHub documents that
  an event that the `GITHUB_TOKEN` causes starts no new workflow run, except
  `workflow_dispatch` and `repository_dispatch`. The completion of a run that
  the `GITHUB_TOKEN` dispatched is such an event, so `workflow_run` does not
  fire. This matches the 0 runs.
- **A: the App token that main already has.**
  `.github/workflows/auto-regen-push.yml:103-106` mints a token with
  `actions/create-github-app-token` from `secrets.REGEN_APP_CLIENT_ID` and
  `secrets.REGEN_APP_PRIVATE_KEY`. Events that an App token causes do start
  workflow runs.
- **A: why the push stays a separate workflow.**
  `.github/workflows/auto-regen-resolve.yml:7-13` says that GitHub takes the
  workflow file of the pull request branch for the resolve run, and that the
  resolve run has no secret and a read-only token. A push job in that file
  would run a file that the pull request controls, with the App secret. So the
  fix keeps the push in its own workflow, which `workflow_run` runs from the
  file on main.
- **B: the cancellation.** `.github/workflows/auto-regen-resolve.yml:36-38`
  sets `group: auto-regen-resolve-${{ github.ref }}` and
  `cancel-in-progress: true`. The API lists 9 of the 13 resolve runs as
  `cancelled`, for example 43624830572, 43624711626, 43624546011, 43624312032
  and 43624125259 on one branch between 18:00 and 18:07 UTC.

## Fix boundary

- **A.** The dispatch job of `.github/workflows/auto-regen.yml` mints a token
  of the regen GitHub App (the same two secrets, no new secret) with
  `permission-actions: write` only, and passes it to
  `scripts/auto-regen.sh dispatch`. The resolve run then has the App as its
  actor, and `workflow_run` starts `auto-regen-push`. The push workflow keeps
  every check that it has: the run is a `workflow_dispatch` of
  `.github/workflows/auto-regen-resolve.yml` in this repository
  (`auto-regen-push.yml:26-30`), the pull request is open and its head is the
  head of the run (`cmd_match_run`), main did not move
  (`auto-regen-push.yml:89-92`), and the patch changes only generated paths
  (`check-patch` at `:96`).
- **B.** The concurrency group of the resolve workflow becomes
  auto-regen-resolve-<pr>-<head> (from the workflow inputs pr and head) with
  `cancel-in-progress: false`. A newer main no longer cancels a run for the
  same head. A newer head of the pull request gets its own group, and the
  push workflow refuses the older result because the head moved. Also,
  `cmd_dispatch` does not dispatch for a pull request head that already has a
  resolve run in the state `queued` or `in_progress`, and it logs one line.
- Nothing else changes: no new workflow, no new secret, no change to the
  find job or to the push job.

## Check

- After the merge, a pull request with a stale generated file gets one resolve
  run and one push run. The push run pushes the regenerated files, and the
  run list of `auto-regen-push.yml` is no longer empty.
- Pushes to main while a resolve run works do not cancel it.

## ADR check

No ADR decides the auto-regen workflows. `rg -n -i "auto-regen" docs/adr`
finds no line. The rule for generated files is in `CLAUDE.md` ("Resolve a
conflict in a generated file ... with a new run of its generator on the merged
code, never by hand"), and this change keeps that rule.

## Open questions

none

Questions considered and answered:

- Should the push move into the resolve workflow? No. The resolve run uses the
  workflow file of the pull request branch (`auto-regen-resolve.yml:7-9`), so a
  push job there would run untrusted code with the App secret.
- Does the fix need a new secret? No. It uses `REGEN_APP_CLIENT_ID` and
  `REGEN_APP_PRIVATE_KEY`, which `auto-regen-push.yml:105-106` already uses.
- Does the App need a new permission? It needs Actions write for the
  dispatch. Task 1.1 checks it. If the App does not have it, the owner grants
  it in the settings of the App, and the task records that it is BLOCKED by
  that grant.
