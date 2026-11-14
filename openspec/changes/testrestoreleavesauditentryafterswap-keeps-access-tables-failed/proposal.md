---
version: 1
---

Issue: #779, #2319.

## Problem

A developer runs the full suite with race and hits two flakes that pass alone: the restore test reports restore not found and the join test gets 200 where it wants 409. The developer needs both tests to pass under load without skip or retry.

## Evidence

- The restore test exists on main. Proof: pkg/store/postgres/postgres_test.go:979
- The join refusal test exists on main. Proof: internal/api/runnerapi/refusal_test.go:176
- Both failures occur only under full-suite load and pass alone. Proof: Issue 779 body: passed 3 of 3 alone, full re-run passed. Issue 2319 body: passes alone, fails in full go test -race runs of 12 to 15 seconds, far below OfflineAfter of 2 minutes.
- The online-name refusal goes through CanRejoin with a 2 minute offline window. Proof: pkg/runner/member.go:211
- The restore keeps live access tables in postgres. Proof: pkg/store/postgres/restore_access.go:126 (func keepAccessTables)

## Fix boundary

- Remove the load dependence of the restore keeps-access test without skipping it.
- Remove the load dependence of the join online-name test so it returns 409 conflict for an online name.

Out of scope:

- Skip or retry of either test
- Change to production code or production behavior in this change; if diagnosis proves a product defect, this change stops and a new change covers it
- Other flaky tests

## Check

- Reproduce both flakes under load and record the cause: the colliding name, the LastSeenAt and clock offset of each join, and the restore row visibility. If the cause is in production code, stop and file a new change. Check: `go test -race -count=50 -run TestRestoreLeavesAuditEntryAfterSwap ./pkg/store/postgres/` with `DUSTPAN_TEST_POSTGRES_URL` set while `go test -race ./...` runs. No route: the test calls the store only.
- Isolate the restore test with a unique restore name per run in pkg/store/postgres/postgres_test.go. Check: `go test -race -count=50 -run TestRestoreLeavesAuditEntryAfterSwap ./pkg/store/postgres/` under full-suite load. No route: the test calls the store only.
- Isolate the join test with a unique runner name per run and a fixed clock per subtest in internal/api/runnerapi/refusal_test.go. Check: `go test -race -count=50 -run TestJoinIsRefused ./internal/api/runnerapi/` under full-suite load. No route: the test uses only its existing test server route.
- Break the guarded behavior in a scratch copy of pkg/runner/member.go and pkg/store/postgres/restore_access.go, then restore it. Check: Both tests fail with the break and pass without it. No route: the test calls the store only.
- Run the full check and confirm no new failure. Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has the restore test that checks the audit entry after the swap. (`pkg/store/postgres/postgres_test.go:979 (func TestRestoreLeavesAuditEntryAfterSwap)`)
- Main has the join refusal test. (`internal/api/runnerapi/refusal_test.go:176 (func TestJoinIsRefused)`)
- Main refuses a join of an online name through CanRejoin. (`pkg/runner/member.go:210 (func Runner.CanRejoin)`)
- Main keeps live access tables in a restore. (`pkg/store/postgres/restore_access.go:126 (func keepAccessTables)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Run the full check and confirm no new failure." is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or "A person's st; a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_model_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: and 1 more.

Questions considered and answered:

- Does any ADR govern these test fixes? No ADR in the pack touches test isolation. The fix changes only test files unless diagnosis proves a product defect, and then this change stops.
- Should the fix skip or retry the tests? No. Both issues forbid skip and retry. The fix adds isolation and a mutation check.
- What causes the load-only failures? The cause is unknown. The first task reproduces both flakes under load and records names, times and clock offsets before isolation lands.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
