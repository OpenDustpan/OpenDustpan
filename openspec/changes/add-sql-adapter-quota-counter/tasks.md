## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing conformance cases for ErrExceeded, first use, released reservation, expired reservation, atomic Record, empty checks, repeated key, window expiry, inline pruning, stale fence, settled plus reserved usage, destroy, lower max, SetEnd, settle idempotency, release of a missing row, and rolled-back Reserve. Check: `go test ./internal/quota/quotatest/...` names the missing SQL cases
- [ ] 1.2 Extend the in-memory reference adapter when the suite needs it. Check: `go test -race ./internal/quota/...` passes on the in-memory adapter

## 2. Adapter

- [ ] 2.1 Add quota tables to the SQLite and PostgreSQL baselines and queries in name order. Check: `make generate && sqlc diff` is clean
- [ ] 2.2 Implement the SQL quota.Tx adapter with fence check, inline expiry delete, inline submit pruning, release, settle, Reserve, Record, window count, and SetEnd. Check: `DUSTPAN_TEST_POSTGRES_URL=<url> go test -race ./internal/quota/...` passes on both dialects
- [ ] 2.3 Prove atomic limit enforcement under concurrent Reserve on one key, with keys locked in sorted order and whole-transaction retry of a serialization failure. Check: concurrency test with two Reserve calls passes on SQLite and PostgreSQL

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run make check with PostgreSQL cases against DUSTPAN_TEST_POSTGRES_URL with zero findings. Check: `make check` reports zero findings
- [ ] 4.2 Add quota tables to the backup table list. Check: `go test -race ./...` passes including backup list check that lists the quota tables
