---
version: 1
---

Issue: #2436.

## Problem

As a runner operator I renew runner sessions and claims, but a renew does not check the namespace of its fence and the declared claim action is never evaluated.

## Evidence

- SessionRenew has a store method in both dialects on main. Proof: pkg/store/sqlite/runner.go:1306 and pkg/store/postgres/runner.go:1272
- RunnerRenew has a store method in both dialects on main. Proof: pkg/store/sqlite/runner.go:1223 and pkg/store/postgres/runner.go:1192
- ActionRunnerClaim is declared on routes in the runner handler package. Proof: internal/api/runnerapi/routes.go:126 and internal/api/runnerapi/plan.go:23
- ServedRoutes exists in the e2e package. Proof: e2e/coverage.go:42 (func ServedRoutes)

## Fix boundary

- SessionRenew and RunnerRenew refuse a fence of another namespace inside the renew update.
- Runner session and runner-key handlers authorize ActionRunnerClaim on the pool at the point of use.
- Add the runner routes to the coverage rows in e2e/coverage.go.

Out of scope:

- Join token and handoff token behavior.
- Runner audit rules beyond the refusals of this change.
- Console views for runners.
- Step-name allowlist, kept with review-follow-ups-october-jobs.

## Check

- Write the conformance test for SessionRenew with a fence of another namespace in pkg/runner/runnertest. Check: TestSessionRenewRefusesFenceOfAnotherNamespace fails on main
- Write the conformance test for RunnerRenew with a fence of another namespace in pkg/runner/runnertest. Check: TestRunnerRenewRefusesFenceOfAnotherNamespace fails on main
- Write the handler tests for a key of another pool and a claim of another job on a session route in the NEW file internal/api/runnerapi/session_claim_test.go. Check: TestSessionRouteRefusesKeyOfAnotherPool and TestSessionRouteRefusesClaimOfAnotherJob fail on main
- Write the coverage test for the runner routes against e2e/coverage.go:42. Check: TestRunnerRoutesAreInServedRoutes fails on main
- Add the fence-namespace refusal to the sqlite adapter in pkg/store/sqlite/runner.go:1306 for SessionRenew and pkg/store/sqlite/runner.go:1223 for RunnerRenew. Check: pkg/runner/runnertest conformance passes on sqlite
- Add the fence-namespace refusal to the postgres adapter in pkg/store/postgres/runner.go:1272 for SessionRenew and pkg/store/postgres/runner.go:1192 for RunnerRenew. Check: pkg/runner/runnertest conformance passes on postgres
- Evaluate ActionRunnerClaim on the stored pool at the point of use for session and runner-key handlers in internal/api/runnerapi/routes.go:126 and internal/api/runnerapi/session.go:446, under the existing route ANY /runner/v1. Check: handler claim tests pass
- Add the runner coverage rows to the existing file e2e/coverage.go:42. Check: the coverage test passes

Depends on:

- `review-follow-ups-october-jobs` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- SessionRenew is a method of the session store port. (`pkg/runner/store.go:217`)
- RunnerRenew is a method of the session store port. (`pkg/runner/store.go:196`)
- The postgres adapter implements SessionRenew. (`pkg/store/postgres/runner.go:1272 (func Runners.SessionRenew)`)
- The postgres adapter implements RunnerRenew. (`pkg/store/postgres/runner.go:1192 (func Runners.RunnerRenew)`)
- stepOf parses the step name of a path. (`internal/api/runnerapi/session.go:446 (func stepOf)`)
- ServedRoutes lists the served routes for coverage. (`e2e/coverage.go:42 (func ServedRoutes)`)
- runnerRoutes wires the runner routes. (`internal/api/runnerapi/routes.go:124 (func Handler.runnerRoutes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_model_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh.

Questions considered and answered:

- Which ADR decisions apply? The pack names no ADR. The change follows the defense-in-depth rule of CLAUDE.md. No owner-tagged decision changes.
- Which code does a claim refusal use, and does it leak pool existence? A claim refusal uses the existing error code runner.invalid with the same body for a wrong pool and a missing pool. This is a closed decision of this change. It adds no new code.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
