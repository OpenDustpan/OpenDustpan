## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add refused-unit rows for SQLite refuseRestoreUnderLock, PostgreSQL refuseRestoreUnderLock, and stack restore under a namespace lock in the NEW file pkg/store/storetest/refused_unit_test.go. Check: `go test ./pkg/store/storetest/ -run TestRefusedUnit -count=1` passes on main and fails when Unit is blanked.
- [ ] 1.2 Add refused-unit rows for workspace create and workspace restore under a namespace lock in the NEW file internal/api/tfe/refused_unit_test.go with an access token. Check: `go test ./internal/api/tfe/ -run TestRefusedUnit -count=1` passes on main and fails when Unit is blanked.
- [ ] 1.3 Add a refused-unit row for checkManualLock through the trigger fire route in the NEW file internal/api/triggers/refused_unit_test.go with an access token. Check: `go test ./internal/api/triggers/ -run TestRefusedUnit -count=1` passes on main and fails when Unit is blanked.
- [ ] 1.4 Use distinct locked and refused objects in the Pulumi answer test and the trigger answer test in NEW files internal/api/pulumi/locked_swap_test.go and internal/api/triggers/locked_swap_test.go. Check: `go test ./internal/api/triggers/ ./internal/api/pulumi/ -run TestLockedSwap -count=1` fails when locked and refused are swapped.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run the affected packages and the full gate; no production code changes. Confirmed by: CI (`make check`). Check: `make check` with zero findings.
