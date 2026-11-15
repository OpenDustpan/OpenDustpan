## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test of the job create source check, which calls CheckSource (NEW), in the NEW file pkg/source/checksource_test.go. Check: TestJobCreateRefusesSourceOutsideFilter fails on main
- [ ] 1.2 Write the failing tests of the pool rule in the NEW file pkg/runner/sweep_pool_test.go. Check: TestSweepTargetRefusesNamespacePool fails on main
- [ ] 1.3 Write the failing tests of the header: scope, region, format, version, in the NEW file internal/unmanaged/report_header_test.go. Check: TestSweepHeaderRefusals fails on main
- [ ] 1.4 Write one failing test for each report bound in the NEW file internal/unmanaged/report_bounds_test.go. Check: TestReportBounds with six subtests fails on main
- [ ] 1.5 Write failing tests of the busy target, a claim without namespace:start-sweep and the audit entry, in the NEW file pkg/runner/sweep_claim_test.go. Check: TestSweepBusyTarget, TestSweepClaimNeedsStartAction, TestSweepFailureWritesAudit fail on main
- [ ] 1.6 SECURITY. Write the hostile test that sends a chunk of another claim straight to the store check, in the NEW file pkg/runner/sweep_chunk_claim_test.go. Check: TestChunkOfAnotherClaimIsRefused fails on main
- [ ] 1.7 SECURITY. Write the hostile test of a filter that changed after create. It sends a claim for a job whose source is now outside the filter straight to CheckSource, in the NEW file pkg/source/checksource_claim_test.go. Check: TestClaimRefusesSourceOutsideNarrowedFilter fails on main

## 2. Build

- [ ] 2.1 Enforce source.Filter in the job service. Add CheckSource (NEW) in the NEW file pkg/source/checksource.go and call it from the job create path. Check: go test ./pkg/source/... passes
- [ ] 2.2 Enforce the pool rule in sweep dispatch in the NEW file pkg/runner/sweep_pool.go. Enforce the header subset in intake in internal/unmanaged/report.go. Check: go test ./pkg/runner/... ./internal/unmanaged/... passes
- [ ] 2.5 SECURITY. Build the per-chunk claim and pool check in the sweep pool code in the NEW file pkg/runner/sweep_pool.go. It refuses a chunk when the claim is not live, is not the claim of the runner, or the pool is not an operator pool with the sweep purpose. Check: go test ./pkg/runner/... -run TestChunkOfAnotherClaimIsRefused passes
- [ ] 2.6 SECURITY. Call CheckSource (NEW) on the job claim path, in the NEW file pkg/source/checksource.go, so that a claim is refused with source.filter-invalid when the stored filter changed after create. Check: go test ./pkg/source/... -run TestClaimRefusesSourceOutsideNarrowedFilter passes
- [ ] 2.3 Check the round trips of the sweep claim flow in internal/servertest. BLOCKED by 130-138-runner-protocol-claims, which adds the claim route under /runner/v1 with the runner key. Check: TestSweepClaimRoundTrips passes once the claim route exists
- [ ] 2.4 Write the NEW guide docs/guides/runners.md. Check: A reviewer runs each command of the guide as written. `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the checks into the existing routes and declare their actions. Check: TestRouteHookReadsTheScopeAndTheEnforcedFields passes
- [ ] 4.2 Add an e2e step to the existing file e2e/source_filter_test.go: PUT /api/dustpan/v1/namespaces/{namespace}/source-filter with an access token, then create a job outside the filter and expect 403 source.filter-invalid. Check: make e2e step passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Add an e2e step in the NEW file e2e/sweep_claim_test.go: claim a sweep on /runner/v1 with a runner key and send a header outside the target. BLOCKED by 130-138-runner-protocol-claims, which adds the claim route. Check: make e2e step passes once the claim route exists. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.4 Run the usability check from the guide alone. Check: Each error names its one command. A person's step.
- [ ] 4.5 Run make check and make docs-lint. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
