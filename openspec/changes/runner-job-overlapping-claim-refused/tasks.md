## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test of overlap refusal with the blocker ID. Check: TestClaimRefusedNamesBlocker fails on main
- [ ] 1.2 Write the failing test that a refused job ends as blocked with no apply. Check: TestRunnerJobBlockedOnRefusal fails on main
- [ ] 1.3 Write the failing test of two parallel overlapping registrations. Check: TestParallelOverlappingClaimsOneWinner fails on main
- [ ] 1.4 Write the failing tests of the audit entry, the release, the apply denial, the stale runner, the final blocked status, the denied refusal reader and the idempotent retry. Check: TestRefusedClaimAuditEntry, TestClaimReleasedAtEnd, TestApplyDeniedWithRefusedClaim, TestStaleRunnerDeniedAfterLeaseLoss, TestBlockedStaysBlockedAfterBlockerEnds, TestRefusedClaimReadDeniedWithoutJobRead and TestRefusedRetrySameIdempotent fail on main

## 2. Implementation

- [ ] 2.1 Add the overlap refusal to the claim registration of the dependency. Check: The register call answers 409 with the blocker claim ID for an overlap
- [ ] 2.2 Move the job to blocked and write the audit entry. Check: A job read shows blocked with the blocker claim ID, and the audit log has one entry

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the refusal into the server and declare the authorization actions of the routes. Check: The route test finds an action and an object for each route
- [ ] 4.2 Add the e2e step with real OpenTofu and a fake provider. Check: make e2e shows two claim records, job C blocked and no provider call from job C
- [ ] 4.3 Write the docs page for refused claims and the blocked status. Check: make docs-lint passes and each command on the page runs
- [ ] 4.4 Run the usability check of the refusal path. Check: A developer finishes the path from the docs page alone, and each error names one command
- [ ] 4.5 Run make check. Check: make check reports zero findings
