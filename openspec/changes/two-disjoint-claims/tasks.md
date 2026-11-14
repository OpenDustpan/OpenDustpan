## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Test for second disjoint claim accepted. Check: TestClaimSecondDisjointAccepted fails on main.
- [ ] 1.2 Test for overlap refused with 409. Check: TestClaimOverlapRefused fails on main.
- [ ] 1.3 Test for empty set. Check: TestClaimEmptySetRejected fails on main.
- [ ] 1.4 Test for touched base and untouched older base. Check: TestClaimTouchedBaseRejected fails on main.
- [ ] 1.5 Test for replace refused. Check: TestClaimReplaceRefused fails on main.
- [ ] 1.6 Test for idempotent retry. Check: TestClaimSameJobRetryIdempotent fails on main.
- [ ] 1.7 Test for missing action. Check: TestClaimMissingActionDenied fails on main.
- [ ] 1.8 Test for list read. Check: TestClaimListShowsBothAccepted fails on main.
- [ ] 1.9 Test for audit entry. Check: TestClaimAcceptAuditEntry fails on main.
- [ ] 1.10 Test for release at job end. Check: TestClaimReleaseAtJobEnd fails on main.
- [ ] 1.11 Test for bad workspace or base. Check: TestClaimBadWorkspaceOrBase fails on main.

## 2. Implementation

- [ ] 2.1 Implement claim record, normalization and release. Start only after the owner accepts the C2 RFC as an ADR. Check: go test ./pkg/workspace/...
- [ ] 2.2 Add concurrency test on each dialect. Check: Parallel registration test passes on SQLite and PostgreSQL.
- [ ] 2.3 Wire the two claim routes with actions in internal/api/runnerapi. Check: Route test shows POST and GET claims declare unit:claim-register and unit:read.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 BLOCKED by the C2 RFC not accepted as an ADR: e2e step with real OpenTofu against POST and GET /api/dustpan/v1/workspaces/{id}/claims with a runner job token credential. Check: e2e finds two claim records in state accepted and no additional version.
- [ ] 4.2 Write the docs page for claims. Check: Docs page lists register and list calls with exact output.
- [ ] 4.3 Run usability check. Check: Developer completes the path from the docs page alone.
- [ ] 4.4 Run full check. Check: make check reports zero findings.
