---
version: 1
---

Issue: #2684.

## Problem

A restored backup can hold runner handoff tokens and sessions whose runner record is absent. Use of those orphaned credentials must fail closed without server errors.

## Evidence

- pkg/store/postgres/restore.go exists on main. Proof: pack paths: pkg/store/postgres/restore.go exists true
- Issue 2684 asks to refuse preserved runner credentials when restored data does not contain their runner. Proof: issue 2684 body: Refuse preserved runner credentials when restored data does not contain their runner

## Fix boundary

- Refuse restored handoff tokens with absent runner at use time on both adapters.
- Refuse restored sessions with absent runner at use time on both adapters.

Out of scope:

- CI jobs
- Secret backend verification
- Denied publish audit

## Check

- Add failing test TestRestoredHandoffTokenWithMissingRunnerIsRefused in pkg/runner/runnertest/cases.go that restores a backup that predates the runner and presents its handoff token. Check: NEW test TestRestoredHandoffTokenWithMissingRunnerIsRefused fails on main
- Add failing test TestRestoredSessionWithMissingRunnerIsRefused in pkg/runner/runnertest/cases.go that presents a restored session. Check: NEW test TestRestoredSessionWithMissingRunnerIsRefused fails on main
- Apply the stable-ID runner-existence check before consumption at handoff-token and session use, shared by both adapters and verified in pkg/store/storetest/backup_runners.go. Check: Both NEW tests pass on sqlite and postgres with runner.not-found
- Wire the refusal through ANY /runner/v1 with a runner key or handoff token credential. Check: ANY /runner/v1 with orphaned credential returns runner.not-found
- Run the usability check for the orphaned-credential refusal. Check: Operator reads runner.not-found and the command that helps
- Run make check with zero findings. Check: `make check` with zero findings

Depends on:

- `review-follow-ups-github-workflows-denied-publish-audit` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks restored runners in the backup conformance suite. (`pkg/store/storetest/backup_runners.go:220 (func checkRestoredRunners)`)
- Main tests single use of handoff tokens in the runner suite. (`pkg/runner/runnertest/cases.go:512 (func testHandoffTokenSingleUse)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Run the usability check for the orphaned-credential refusal." is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Run make check with zero findings." is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or "A person's step."; a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: and 2 more.

Questions considered and answered:

- Should restore delete orphaned runner credentials instead of refusing them at use time? No. The pack lists no ADR, and issue 2684 asks for refusal at use time on both adapters, not a change to backup contents or a new migration.
- Where does the refusal apply? Use time on both adapters. The roadmap scope says so, and main already checks restored runners in pkg/store/storetest/backup_runners.go.
- Does any ADR govern this refusal? No ADR in the pack touches runner restore or credential refusal, so no ADR quote applies; the change follows the fail-closed rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
