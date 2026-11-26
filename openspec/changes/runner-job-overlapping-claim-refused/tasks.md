## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test of overlap refusal with the blocker ID in `internal/api/runnerapi/claimer_test.go`. Check: NEW test `TestClaimRefusedNamesBlocker` fails on main
- [ ] 1.2 Write the failing test that a refused job ends as blocked with no apply in `pkg/runner/runner_test.go`. Check: NEW test `TestRunnerJobBlockedOnRefusal` fails on main
- [ ] 1.3 Write the failing test of two parallel overlapping registrations in `internal/api/runnerapi/claimer_test.go`. Check: NEW test `TestParallelOverlappingClaimsOneWinner` fails on main
- [ ] 1.4 Write the failing tests of the audit entry, the release, the apply denial, the stale runner, the final blocked status, the denied refusal reader and the idempotent retry in `internal/api/runnerapi/flow_test.go`. Check: NEW tests `TestRefusedClaimAuditEntry`, `TestClaimReleasedAtEnd`, `TestApplyDeniedWithRefusedClaim`, `TestStaleRunnerDeniedAfterLeaseLoss`, `TestBlockedStaysBlockedAfterBlockerEnds`, TestRefusedClaimReadDeniedWithoutJobRead and `TestRefusedRetrySameIdempotent` fail on main

## 2. Implementation

- [ ] 2.1 Add the overlap refusal to the claim registration of the dependency in `internal/api/runnerapi/claimer.go` (NEW file). Check: The register call answers 409 with the blocker claim ID for an overlap
- [ ] 2.2 Move the job to blocked and write the audit entry in the existing file `pkg/runner/coordinator.go`. Check: A job read shows blocked with the blocker claim ID, and the audit log has one entry

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Wire the refusal into the server and declare the authorization actions of the routes. Check: The route test finds an action and an object for each route
- [ ] 4.2 Add the e2e step with real OpenTofu and a fake provider in the existing file `e2e/claim_gate_test.go`. It calls the existing route POST /runner/v1/workspaces/{id}/claims with the access token of the local wrapper (the harness issues no runner job token). Check: make e2e shows two claim records, job C blocked and no provider call from job C.
- [ ] 4.3 Write the docs page for refused claims and the blocked status in the NEW file `docs/guides/refused-claims.md`. Check: make docs-lint passes and each command on the page runs
- [ ] 4.4 Run the usability check of the refusal path. Check: A developer finishes the path from the docs page alone, and each error names one command.
- [ ] 4.5 Run make check. Check: make check reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
