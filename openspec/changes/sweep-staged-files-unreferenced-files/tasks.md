## 1. Failing tests first

- [ ] 1.1 Write the test for a staged file older than 24 hours and a younger one Check: TestRegistrySweepDeletesStagedByAge fails on main
- [ ] 1.2 Write the test that keeps a shared content blob Check: TestRegistrySweepKeepsSharedContentBlob fails on main
- [ ] 1.3 Write the test for a takeover that resumes and a stale fencing token Check: TestRegistrySweepTakeoverResumes fails on main
- [ ] 1.4 Write the tests for a publish during the sweep, a moved staged file, a stale mark and a failed store delete Check: TestRegistrySweepPublishRace, TestRegistrySweepMovedStagedKept, TestRegistrySweepStaleMarkExpires and TestRegistrySweepDeleteFailureRetries fail on main
- [ ] 1.5 Write the tests for maintenance mode, a restore that writes a blob, a listing error and an unknown fresh key Check: TestRegistrySweepMaintenancePause, TestRegistrySweepKeepsRestoredBlob, TestRegistrySweepStoreErrorRetries and TestRegistrySweepKeepsFreshUnknownKey fail on main
- [ ] 1.6 Write the conformance test with a blob store that lists many keys Check: TestRegistrySweepManyKeysConformance fails on main

## 2. Implementation

- [ ] 2.1 Implement the schedule row, the durable run, the paged listing, the mark table and the cursor in each store baseline Check: `go test ./pkg/registry/... -run TestRegistrySweep -count=1` passes
- [ ] 2.2 Expose the metric with installation counters Check: `curl /metrics | grep registry_sweep` shows the counters and the last run

## 3. Definition of done

- [ ] 3.1 Wire the sweep in the server start, in the worker component Check: The sweep starts in `opendustpan serve` and a wiring test passes
- [ ] 3.2 Run an e2e step with the real directory: upload, refused publish, sweep Check: `make e2e` registry scenario passes
- [ ] 3.3 Write the docs page and do the usability check from the page alone Check: The docs commands run in a test and `make check` reports zero findings
