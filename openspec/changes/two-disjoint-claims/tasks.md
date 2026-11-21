## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Test for second disjoint claim accepted, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimSecondDisjointAccepted fails on main.
- [x] 1.2 Test for overlap refused with 409, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimOverlapRefused fails on main.
- [x] 1.3 Test for empty set, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimEmptySetRejected fails on main.
- [x] 1.4 Test for touched base and untouched older base, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimTouchedBaseRejected fails on main.
- [x] 1.5 Test for replace refused, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimReplaceRefused fails on main.
- [x] 1.6 Test for idempotent retry, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimSameJobRetryIdempotent fails on main.
- [x] 1.7 Test for missing action, in NEW file internal/api/runnerapi/claims_route_test.go. Check: TestClaimMissingActionDenied fails on main.
- [x] 1.8 Test for list read, in NEW file internal/api/runnerapi/claims_route_test.go. Check: TestClaimListShowsBothAccepted fails on main.
- [x] 1.9 Test for audit entry, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimAcceptAuditEntry fails on main.
- [x] 1.10 Test for release at job end, in pkg/durable/runner_claimed_test.go. Check: TestClaimReleaseAtJobEnd fails on main.
- [x] 1.11 Test for bad workspace or base, in NEW file pkg/workspace/claim_register_test.go. Check: TestClaimBadWorkspaceOrBase fails on main.
- [x] 1.12 NEW. Test that the token of another job, and the token of an ended apply claim, are refused, in NEW file internal/api/runnerapi/claims_route_test.go. Check: NEW test TestClaimTokenBoundToJobAndClaim fails on main.

## 2. Implementation

- [x] 2.1 Implement claim record, normalization and release in NEW file pkg/workspace/claim_register.go. Start only after ADR 368 and ADR 369 are on main. Check: go test ./pkg/workspace/...
- [x] 2.2 Add concurrency test on each dialect, in NEW file pkg/workspace/claim_register_test.go. Check: Parallel registration test passes on SQLite and PostgreSQL.
- [x] 2.3 Wire the two claim routes with actions in internal/api/runnerapi. Check: Route test shows POST and GET claims declare unit:claim-register and unit:read.

## 3. Side effects (computed from main)

- [x] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 BLOCKED by the missing claims routes and the missing real OpenTofu e2e harness on main. E2e step in NEW file e2e/claims_disjoint_test.go against POST and GET /api/dustpan/v1/workspaces/{id}/claims with a runner job token credential. Check: e2e finds two claim records in state accepted and no additional version.
- [x] 4.2 Write the docs page for claims in NEW file docs/guides/claims.md. Check: Docs page lists register and list calls with exact output. `make docs-lint` passes on each edited guide.
- [ ] 4.3 Agent pre-run (human run-through in person-steps (two-disjoint-claims 4.3)): Run usability check. Check: Developer completes the path from the docs page alone. Record the transcript in the pull request body.
- [ ] 4.4 Run full check. Check: make check reports zero findings.
