## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test of the job create source check, which calls CheckSource. Check: TestJobCreateRefusesSourceOutsideFilter fails on main
- [ ] 1.2 Write the failing tests of the pool rule. Check: TestSweepTargetRefusesNamespacePool fails on main
- [ ] 1.3 Write the failing tests of the header: scope, region, format, version. Check: TestSweepHeaderRefusals fails on main
- [ ] 1.4 Write one failing test for each report bound. Check: TestReportBounds with six subtests fails on main
- [ ] 1.5 Write failing tests of the busy target, a claim without namespace:start-sweep and the audit entry. Check: TestSweepBusyTarget, TestSweepClaimNeedsStartAction, TestSweepFailureWritesAudit fail on main

## 2. Build

- [ ] 2.1 Enforce source.Filter in the job service. Check: go test ./pkg/source/... passes
- [ ] 2.2 Enforce the pool rule and the header subset in sweep dispatch and intake. Check: go test ./pkg/runner/... ./internal/unmanaged/... passes
- [ ] 2.3 Check the round trips of the sweep claim flow in internal/servertest. BLOCKED by 130-138-runner-protocol-claims, which adds the claim route under /runner/v1 with the runner key. Check: TestSweepClaimRoundTrips passes once the claim route exists
- [ ] 2.4 Write the NEW guide docs/guides/runners.md. Check: A reviewer runs each command of the guide as written

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the checks into the existing routes and declare their actions. Check: TestRouteHookReadsTheScopeAndTheEnforcedFields passes
- [ ] 4.2 Add an e2e step: PUT /api/dustpan/v1/namespaces/{namespace}/source-filter with an access token, then create a job outside the filter and expect 403 source.filter-invalid. Check: make e2e step passes
- [ ] 4.3 Add an e2e step: claim a sweep on /runner/v1 with a runner key and send a header outside the target. BLOCKED by 130-138-runner-protocol-claims, which adds the claim route. Check: make e2e step passes once the claim route exists
- [ ] 4.4 Run the usability check from the guide alone. Check: Each error names its one command
- [ ] 4.5 Run make check and make docs-lint. Check: make check with zero findings
