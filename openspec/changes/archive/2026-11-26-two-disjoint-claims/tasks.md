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
- [x] 2.4 Add the claim store port (`workspace.ClaimStore`), its in-memory implementation and the SQLite and PostgreSQL adapters (tables `workspace_claims` and `workspace_claim_touches` in both baselines, queries in `internal/store/sqlite/queries.sql` and `internal/store/postgres/queries.sql`, purge and backup coverage). Check: `go test ./pkg/workspace -run TestMemoryClaimStoreConformance`, `go test ./internal/store/sqlite -run TestClaimStoreConformance` and `go test ./internal/store/postgres -run TestClaimStoreConformance` pass; `sqlc diff` is clean.
- [x] 2.5 Wire the claim registry over the built-in store in `pkg/dustpan/builtin_stores.go` and end the claims of a job when the job ends. Check: `go test ./pkg/dustpan -run TestClaimReleasingClaimer` passes, No route: the test ends the claims of a job through the registry only. The e2e step of 4.1 reaches the routes on a real server.
- [x] 2.6 Give a claim of the local wrapper a lease, in `pkg/workspace/claim_register.go`, `pkg/workspace/claim_store.go`, `internal/api/runnerapi/claims.go`, `internal/store/sqlite/workspace_claims.go`, `internal/store/postgres/workspace_claims.go` and `pkg/dustpan/claim_sweep.go`: store the end of the lease and the time of the last renewal (`lease_ends_at_ms` and `renewed_at_ms` in both baselines), add `POST /runner/v1/workspaces/{id}/claims/{claimID}/renew` (action `unit:claim-register`), and end the claims whose lease passed on a schedule of the server (`claim-expire`, every 30 seconds, at most 500 claims in one run), with an audit entry by the server for each end. Check: `go test ./pkg/workspace ./internal/api/runnerapi ./internal/store/codec ./pkg/dustpan -run 'Claim'` passes, and `go test ./internal/store/sqlite ./internal/store/postgres -run TestClaimStoreConformance` passes.
- [x] 2.7 Harden the lease, in `pkg/workspace/claim_register.go` (`Renew`), `internal/store/sqlite/workspace_claims.go` (`RenewClaim`), `internal/store/postgres/workspace_claims.go` and `internal/api/runnerapi/claims.go`: the renew ownership check in each layer (statement, store, registry), the table CHECK `(job_id = '') = (lease_ends_at_ms > 0)` in both baselines, a rotating first namespace in each sweep run, and 404 for a caller without the action on a deleted workspace. Check: `go test ./pkg/workspace ./pkg/dustpan ./internal/api/runnerapi -run 'Claim|Renew'`, `go test ./internal/store/sqlite ./internal/store/postgres -run 'Claim|Renew'` pass.

## 3. Side effects (computed from main)

- [x] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [x] 4.1 E2e step in NEW file e2e/claims_disjoint_test.go against POST and GET /runner/v1/workspaces/{id}/claims with the access token of the local wrapper (the harness issues no runner job token). Check: `go test -tags e2e ./e2e -run TestJobsAPI` passes on SQLite and PostgreSQL: two claim records in state accepted, an overlapping third claim refused with 409. The step does not count the state versions; the claims route has no path that writes one.
- [x] 4.1b E2e step in NEW file e2e/claims_expiry_test.go: the wrapper registers a claim, renews it, an overlapping claim is refused, the sweep ends the claim after the lease, and the overlapping claim is then accepted. The server of the test sets a lease of 5 seconds and a sweep period of 500 milliseconds through a test option. Check: `go test -tags e2e ./e2e -run TestClaimsLeaseExpires` passes on SQLite and PostgreSQL.
- [x] 4.2 Write the docs page for claims in NEW file docs/guides/claims.md. Check: Docs page lists register and list calls with exact output. `make docs-lint` passes on each edited guide.
- [x] 4.4 Run full check. Check: make check reports zero findings. Confirmed by: CI of #3909 (all checks green on head b48dece00): https://github.com/OpenDustpan/OpenDustpan/actions/runs/44961709765
