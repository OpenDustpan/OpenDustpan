## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the test for a staged file older than 24 hours and a younger one in `pkg/registry/sweep_test.go` (NEW). Check: TestRegistrySweepDeletesStagedByAge fails on main
- [ ] 1.2 Write the test that keeps a shared content blob in `pkg/registry/sweep_test.go` (NEW). Check: TestRegistrySweepKeepsSharedContentBlob fails on main
- [ ] 1.3 Write the test for a takeover that resumes and a stale fencing token in `pkg/registry/sweep_test.go` (NEW). Check: TestRegistrySweepTakeoverResumes fails on main
- [ ] 1.4 Write the tests for a publish that starts before any mark exists, a publish that meets a live mark, a moved staged file, a stale mark, a failed store delete and a failed mark removal in `pkg/registry/sweep_test.go` (NEW). Check: TestRegistrySweepPublishRace, TestRegistrySweepPublishBeforeMark, TestRegistrySweepMovedStagedKept, TestRegistrySweepStaleMarkExpires, TestRegistrySweepDeleteFailureRetries and TestRegistrySweepMarkRemovalFailure fail on main
- [ ] 1.5 Write the tests for a staged key with no row, a staged row with the new creation time, maintenance mode, a restore that writes a blob, a listing error and an unknown fresh key in `pkg/registry/sweep_test.go` (NEW). Check: TestRegistrySweepStagedNoRow, TestRegistrySweepStagedCreatedAt, TestRegistrySweepMaintenancePause, TestRegistrySweepKeepsRestoredBlob, TestRegistrySweepStoreErrorRetries and TestRegistrySweepKeepsFreshUnknownKey fail on main
- [ ] 1.7 SECURITY. Write the hostile test that sends a key outside the registry prefix straight to the blob delete and the key listing of the port adapter, in `pkg/registry/sweep_test.go` (NEW). Check: TestRegistrySweepRefusesForeignPrefix fails on main
- [ ] 1.8 SECURITY. Write the hostile test that runs the delete step with a stale fencing token straight against the service layer, in `pkg/registry/sweep_test.go` (NEW). Check: TestRegistrySweepStaleTokenDeletesNothing fails on main
- [ ] 1.6 Write the conformance test with a blob store that lists many keys in `pkg/registry/sweep_conformance_test.go` (NEW). Check: TestRegistrySweepManyKeysConformance fails on main

## 2. Implementation

- [ ] 2.1 Edit the baseline of each dialect (`pkg/store/<dialect>/migrations/<NNNN>_baseline.sql`): staged created_at_ms and moved flag, content-key row table, mark table and cursor table. Check: `go test ./pkg/store/... -run TestFrozenMigrations -count=1` passes with the new baseline hash
- [ ] 2.2 Implement the schedule row, the durable run, the paged listing and the publish lock in `pkg/registry/sweep.go` (NEW). Check: `go test ./pkg/registry/... -run TestRegistrySweep -count=1` passes
- [ ] 2.4 SECURITY. Build the prefix guard of the port adapter in `pkg/registry/sweep.go` (NEW). The blob delete and the key listing accept only keys of the registry prefix. Check: `go test ./pkg/registry/... -run TestRegistrySweepRefusesForeignPrefix -count=1` passes
- [ ] 2.5 SECURITY. Build the fencing token check of the delete step in `pkg/registry/sweep.go` (NEW). The check runs in the delete transaction. Check: `go test ./pkg/registry/... -run TestRegistrySweepStaleTokenDeletesNothing -count=1` passes
- [ ] 2.3 Expose the metric with installation counters in `pkg/registry/sweep_metrics.go` (NEW). Check: NEW metrics series registry_sweep_* appear in the server metrics output, checked by a metrics test

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the sweep in the server start, in the worker component Check: The sweep starts in `opendustpan serve` and a wiring test passes
- [ ] 4.2 Run an e2e step with the real directory: upload through POST /api/dustpan/v1/namespaces/{}/registry/uploads with a personal token, a refused publish through PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{}, then the sweep Check: `make e2e` registry scenario passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Write the NEW docs page `docs/guides/registry-sweep.md` and do the usability check from the page alone. The page states the 24-hour age, the reference rule, the 1-hour mark expiry and the pause in maintenance mode. Check: The docs commands run in a test and `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only). `make docs-lint` passes on each edited guide.
