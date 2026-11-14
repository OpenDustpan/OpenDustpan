## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing conformance cases in `internal/quota/quotatest/quotatest.go` for ErrExceeded, first use, released reservation, expired reservation, atomic Record, empty checks, repeated key, window expiry, inline pruning, stale fence, settled plus reserved usage, destroy, lower max, SetEnd, settle idempotency, release of a missing row, and rolled-back Reserve. Check: `go test ./internal/quota/quotatest/...` names the missing SQL cases
- [ ] 1.2 Extend the in-memory reference adapter in `internal/quota/quotatest/mem_test.go` when the suite needs it. Check: `go test -race ./internal/quota/...` passes on the in-memory adapter

## 2. Adapter

- [ ] 2.1 Add quota tables to the SQLite and PostgreSQL baselines and queries in name order, in `pkg/store/sqlite/migrations/0168_baseline.sql`, `pkg/store/postgres/migrations/0168_baseline.sql` and the `queries.sql` file of each dialect (`pkg/store/sqlite/queries.sql`, `pkg/store/postgres/queries.sql`); `make generate` writes `pkg/store/<dialect>/internal/db`. Add the quota tables to the backup table list in `pkg/backup`. Check: `make generate && sqlc diff` is clean. Confirmed by: the driver's gates.
- [ ] 2.2 Implement quota.Counter and quota.Tx in the store of each dialect, NEW files `pkg/store/sqlite/quota.go` and `pkg/store/postgres/quota.go` (SQL adapters live in pkg/store/<dialect>), with fence check, inline expiry delete, inline submit pruning, release, settle, Reserve, Record, window count, and SetEnd. Check: `go test -race ./pkg/store/sqlite/...` and `DUSTPAN_TEST_POSTGRES_URL=<url> go test -race -timeout 60m ./pkg/store/postgres/...` run quotatest.Run and pass
- [ ] 2.3 Prove atomic limit enforcement in NEW test files `pkg/store/sqlite/quota_test.go` and `pkg/store/postgres/quota_test.go` under concurrent Reserve on one key, with keys locked in sorted order and whole-transaction retry of a serialization failure. Check: concurrency test with two Reserve calls passes on SQLite and PostgreSQL

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run make check with PostgreSQL cases against DUSTPAN_TEST_POSTGRES_URL with zero findings. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Add quota tables to the backup table list. Check: `go test -race ./...` passes including backup list check that lists the quota tables
