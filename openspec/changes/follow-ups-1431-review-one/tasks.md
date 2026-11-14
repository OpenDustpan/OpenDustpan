## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add the NEW CheckStagingSchema entry guard in pkg/store/postgres/backup_item_keys.go and call it before the staged restore check path at pkg/store/postgres/backup.go:1089, with a failing test for empty, leading-digit, and 64-char names and a passing 63-char name. Check: TestStagingNameEntryRejectsHostileSchema in pkg/store/postgres fails on main, passes after the fix.
- [ ] 1.2 Add the NEW pkg/job.CheckChunkField helper for pkg/job/record.go:392 and pkg/store/internal/jobstore/record.go:772, with a failing test that domain and store agree on hostile chunk rows with unchanged errors on postgres and sqlite staged paths. Check: TestChunkRuleSharedByDomainAndStore in pkg/job fails on main, passes after the fix.
- [ ] 1.3 After release-blocker-item-key-sealer lands, re-run the Versions caller search to confirm the rewrap adds no caller, then remove ItemKeyBlobs.Versions in pkg/secret/itemkeyblob.go:233 and update only its test callers. Check: `rg -n -F --glob '!*_test.go' 'ItemKeyBlobs.Versions' pkg internal cmd` shows no caller after release-blocker-item-key-sealer, then no definition or caller after removal.
- [ ] 1.4 Add a scan test in pkg/store/postgres that lists stagingTable uses outside the guarded helpers. Check: TestStagingTableUsesStayGuarded in pkg/store/postgres fails when a new direct stagingTable use appears.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run the affected package unit tests with race, plus the new stagingTable-use scan test. Check: go test -race ./pkg/store/postgres/ ./pkg/job/ ./pkg/secret/
- [ ] 3.2 Run the full check gate with zero findings. Confirmed by: CI (make check). Check: `make check`
