## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the test for a staged file older than 24 hours and a younger one Check: TestRegistrySweepDeletesStagedByAge fails on main
- [ ] 1.2 Write the test that keeps a shared content blob Check: TestRegistrySweepKeepsSharedContentBlob fails on main
- [ ] 1.3 Write the test for a takeover that resumes and a stale fencing token Check: TestRegistrySweepTakeoverResumes fails on main
- [ ] 1.4 Write the tests for a publish that starts before any mark exists, a publish that meets a live mark, a moved staged file, a stale mark, a failed store delete and a failed mark removal Check: TestRegistrySweepPublishRace, TestRegistrySweepPublishBeforeMark, TestRegistrySweepMovedStagedKept, TestRegistrySweepStaleMarkExpires, TestRegistrySweepDeleteFailureRetries and TestRegistrySweepMarkRemovalFailure fail on main
- [ ] 1.5 Write the tests for a staged key with no row, a staged row with the new creation time, maintenance mode, a restore that writes a blob, a listing error and an unknown fresh key Check: TestRegistrySweepStagedNoRow, TestRegistrySweepStagedCreatedAt, TestRegistrySweepMaintenancePause, TestRegistrySweepKeepsRestoredBlob, TestRegistrySweepStoreErrorRetries and TestRegistrySweepKeepsFreshUnknownKey fail on main
- [ ] 1.6 Write the conformance test with a blob store that lists many keys Check: TestRegistrySweepManyKeysConformance fails on main

## 2. Implementation

- [ ] 2.1 Edit the baseline of each dialect: staged created_at_ms and moved flag, content-key row table, mark table and cursor table Check: `go test ./pkg/store/... -run TestFrozenMigrations -count=1` passes with the new baseline hash
- [ ] 2.2 Implement the schedule row, the durable run, the paged listing and the publish lock Check: `go test ./pkg/registry/... -run TestRegistrySweep -count=1` passes
- [ ] 2.3 Expose the metric with installation counters Check: NEW metrics series registry_sweep_* appear in the server metrics output, checked by a metrics test

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the sweep in the server start, in the worker component Check: The sweep starts in `opendustpan serve` and a wiring test passes
- [ ] 4.2 Run an e2e step with the real directory: upload through POST /api/dustpan/v1/namespaces/{}/registry/uploads with a personal token, a refused publish through PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{}, then the sweep Check: `make e2e` registry scenario passes
- [ ] 4.3 Write the docs page and do the usability check from the page alone Check: The docs commands run in a test and `make check` reports zero findings
