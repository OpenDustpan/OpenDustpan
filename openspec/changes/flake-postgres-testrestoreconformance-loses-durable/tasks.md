## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing test first

- [ ] 1.1 Run the loaded case in pkg/store/postgres/postgres_test.go:932 on main with a CPU-heavy job beside it and record the lease failure Check: `go test -race -timeout 60m ./pkg/store/postgres/ -run TestRestoreConformance` fails with the lost claim text on main under load

## 2. Fix

- [ ] 2.1 In the lease setup used by pkg/store/storetest/restore.go:58, set the lease to 10 minutes with 30 second renewal except the swap lock-conflict case in pkg/store/postgres/restore_swap_conflict_internal_test.go:36 which keeps the production lease Check: `go test -race -timeout 60m ./pkg/store/postgres/ -run TestRestoreConformance` passes alone
- [ ] 2.2 Run pkg/store/postgres/postgres_test.go:932 five times beside a CPU-heavy job Check: five passes with DUSTPAN_TEST_POSTGRES_URL set

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run make check for pkg/store/storetest/restore.go:58 Confirmed by: CI (make check) Check: `make check` with zero findings
