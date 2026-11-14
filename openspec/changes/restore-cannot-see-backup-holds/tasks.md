## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing manifest test for the recorded baseline SHA-256 in pkg/backup/restore_check_test.go. Check: NEW test TestManifestHoldsBaselineHash fails on main, passes after fix.
- [ ] 1.2 Add failing restore test for mismatched and missing SHA-256 with no writes in pkg/backup/restore_check_test.go. Check: NEW test TestRestoreRefusesBaselineMismatch fails on main, passes after fix.

## 2. Build

- [ ] 2.1 Record the baseline SHA-256 in the backup manifest in pkg/backup/service.go. Check: Manifest holds 64 lowercase hex hash.
- [ ] 2.2 Compare SHA-256 at restore enter and refuse with the NEW error code in pkg/backup/restore_service.go. Check: Mismatch, missing, and malformed hash refuse with NEW code restore.baseline-mismatch.
- [ ] 2.3 Write an audit entry for each refused restore through the checked port in pkg/backup/restore_service.go after write-audit-entry-when-person lands. Check: Audit entry exists for each refusal through the checked append port from write-audit-entry-when-person.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire route ANY /api/server/restores to the check with its installation restore grant, called with an access token. Check: An access-token call without the grant is denied. Confirmed by: CI (make e2e restore step).
- [ ] 4.2 Add the docs page NEW docs/guides/backup-restore-baseline.md with the code restore.baseline-mismatch and the remedy to make a fresh backup. Check: A person's step.
- [ ] 4.3 Run the usability check and make check with zero findings. Check: Confirmed by: CI (make check).
