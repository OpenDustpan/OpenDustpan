## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 Write failing conformance cases in `internal/quota/quotatest/quotatest.go` for ErrExceeded, first use, released reservation, expired reservation, atomic Record, empty checks, repeated key, window expiry, inline pruning, stale fence, settled plus reserved usage, destroy, lower max, SetEnd, and rolled-back Reserve. The end of a reservation keeps the cases that main's suite has: `testSettle` (a repeat settle changes nothing; a release of a settled reservation is `quota.ErrEnded`), `testRelease` (a repeat release changes nothing; a settle of a released reservation is `quota.ErrEnded`) and `testUnknown` (an unknown ID is `quota.ErrNotFound`) in `internal/quota/quotatest/quotatest.go`; do not change their expected errors. Add the NEW case `testReleaseExpired` to the case table of `Run` in that file and to the in-memory reference adapter of task 1.2: reserve, let the reservation pass its end time, reserve again on the same key at its limit (the expired row does not count, so it succeeds), release the expired reservation (no error), then release it again (no error, no change to usage). Check: `go test ./internal/quota/quotatest/...` names the missing SQL cases
- [x] 1.2 Extend the in-memory reference adapter in `internal/quota/quotatest/mem_test.go` when the suite needs it. Check: `go test -race ./internal/quota/...` passes on the in-memory adapter

## 2. Adapter

- [ ] 2.1 Add the four tables of design decision 4 (quota_keys, quota_reservations, quota_reservation_keys, quota_requests) with their columns, checks, keys and indexes to `pkg/store/sqlite/migrations/0168_baseline.sql` and `pkg/store/postgres/migrations/0168_baseline.sql`, next to the self-service tables in name order, and update their `.sql.sha256` files. Add the 15 queries of decision 4, in name order in one quota block, to `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql`; `make generate` writes `pkg/store/sqlite/internal/db` and `pkg/store/postgres/internal/db`. Add the four tables to the portable backup list `pkg/backup/portable_tables.go`. Check: `make generate && sqlc diff` is clean, and `go test ./pkg/store/sqlite -run TestFrozenMigrations -count=1` passes. Confirmed by: the driver's gates.
- [ ] 2.2 Implement quota.Counter and quota.Tx in the store of each dialect, NEW files `pkg/store/sqlite/quota.go` and `pkg/store/postgres/quota.go` (SQL adapters live in pkg/store/<dialect>), with fence check, inline expiry (an expired reservation does not count, and its row stays until a release or the reconcile), inline submit pruning, release, settle, Reserve, Record, window count, and SetEnd. Check: `go test -race ./pkg/store/sqlite/...` and `DUSTPAN_TEST_POSTGRES_URL=<url> go test -race -timeout 60m ./pkg/store/postgres/...` run quotatest.Run and pass
- [ ] 2.3 Prove atomic limit enforcement in NEW test files `pkg/store/sqlite/quota_test.go` and `pkg/store/postgres/quota_test.go` under concurrent Reserve on one key, with keys locked in sorted order and whole-transaction retry of a serialization failure. Check: concurrency test with two Reserve calls passes on SQLite and PostgreSQL

## 3. Side effects (computed from main)

- [ ] 3.1 Confirm that no request flow changed, so the round-trip budgets stand: the change adds store adapters and no route. Check: the round-trip tests in `internal/servertest/roundtrips_test.go` pass unchanged. Confirmed by: CI (the round-trip tests pass unchanged).
- [x] 3.2 Regenerate `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run make check with PostgreSQL cases against DUSTPAN_TEST_POSTGRES_URL with zero findings. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Prove the backup holds the quota tables: the coverage check `pkg/backup/coverage.go:104` (func CheckColumns) passes with the four tables of task 2.1. Check: `go test -race ./pkg/backup/... ./pkg/store/...` passes
