## 1. Failing tests

- [ ] 1.1 Write failing conformance cases for ErrExceeded, first use, duplicate ID, released reservation, expired reservation, atomic Record, window expiry, stale fence, SetEnd, settle idempotency, and rolled-back Reserve. Check: `go test ./internal/quota/quotatest/...` names the missing SQL cases
- [ ] 1.2 Extend the in-memory reference adapter when the suite needs it. Check: `go test -race ./internal/quota/...` passes on the in-memory adapter

## 2. Adapter

- [ ] 2.1 Add quota tables to the SQLite and PostgreSQL baselines and queries in name order. Check: `make generate && sqlc diff` is clean
- [ ] 2.2 Implement the SQL quota.Tx adapter with fence check, inline expiry delete, release, settle, Reserve, Record, window count, and SetEnd. Check: `DUSTPAN_TEST_POSTGRES_URL=<url> go test -race ./internal/quota/...` passes on both dialects
- [ ] 2.3 Prove atomic limit enforcement under concurrent Reserve on one key, with keys locked in sorted order and one retry of a serialization failure for multi-key checks. Check: concurrency test with two Reserve calls passes on SQLite and PostgreSQL
- [ ] 2.4 Wire the SQL counter into catalog.Store.Submit so the submit transaction uses quota.Tx. Check: `go test ./internal/selfservice/...` passes with the SQL counter as the default counter

## 3. Definition of done

- [ ] 3.1 Run make check with PostgreSQL cases against DUSTPAN_TEST_POSTGRES_URL with zero findings. Check: `make check` reports zero findings
- [ ] 3.2 Add quota tables to the backup table list. Check: `go test -race ./...` passes including backup list check that lists the quota tables
