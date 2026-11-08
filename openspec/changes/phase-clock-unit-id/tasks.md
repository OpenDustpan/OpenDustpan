## 1. The job body

- [ ] 1.1 Add `NamespaceID` to `unitBody` and to `encodeUnit` and `decodeUnit` in `pkg/store/internal/jobstore/body.go`. Check: `TestBodyRoundTrip` in `pkg/store/internal/jobstore/body_test.go`.
- [ ] 1.2 Set the namespace ID at job start in `pkg/job/service_start.go` from the namespace record that `FenceSource` reads. Keep the name. Check: `TestStartRecordsTheNamespaceID` in `pkg/job`.
- [ ] 1.3 Add a namespace ID method to `FenceSource` in `pkg/job/service.go` and implement it on `jobFences` in `pkg/dustpan/jobservice.go`. Check: the job tests build and the stored body has the ID.

## 2. The phase clock and the manual lock path

- [ ] 2.1 Compare the unit ID and the namespace ID in `pkg/job/phaseclock.go`. Use the name only in the error text. Check: `TestManualLockPhaseStartAfterRename` in `pkg/dustpan/manual_lock_phases_test.go` and the case "another unit with the same name" in `pkg/job/phaseclock_test.go`.
- [ ] 2.2 Add a `Unit` value with the reference and the two IDs in `pkg/manuallock/manuallock.go`. Take it in `PhaseClock`, `PhaseStart`, `afterPhase`, `CoveringFor` and `NamespaceLockFor`. Check: `TestCoveringForUsesTheUnitIDs` in `pkg/manuallock`, and the manual lock service tests pass.
- [ ] 2.3 Resolve the IDs in `pkg/stack/updates.go` and `pkg/workspace/service.go` and pass them. Check: a renamed stack and a renamed workspace take the lock in the tests.
- [ ] 2.4 Keep the create path for a unit that does not exist yet at its current behavior, and add the case to the test. Check: the create case of the manual lock tests passes.

## 3. Documentation and verification

- [ ] 3.1 Add one sentence to `docs/guides/manual-locks.md`: a job phase that started before a lock passes it after a rename of its unit. Check: `make docs-lint` exits 0.
- [ ] 3.2 Run `go test ./pkg/job/... ./pkg/manuallock/... ./pkg/store/internal/jobstore/... ./pkg/dustpan/...` and both store packages with `-timeout 60m`. Check: the packages pass.
- [ ] 3.3 Run `make check`. Check: zero findings.
