## 1. Schema

- [x] 1.1 Confirm the six baseline lines and the query rows of `proposal.md` on
  the checkout. Check: each `path:line` quotes the line that it names.
- [x] 1.2 Edit `pkg/store/sqlite/migrations/0168_baseline.sql`: make
  `backups.ended_at_ms`, `restores.ended_at_ms` and
  `store_checks.ended_at_ms` `INTEGER` with no `NOT NULL` and no default. Add
  no numbered migration. Check: `rg -n "ended_at_ms"` on the baseline shows no
  `DEFAULT 0` for these three tables, and `git diff` holds one file.
- [x] 1.3 Write the new SHA-256 in
  `pkg/store/sqlite/migrations/0168_baseline.sql.sha256`. Check:
  `sha256sum pkg/store/sqlite/migrations/0168_baseline.sql` matches the file.

## 2. Queries and adapters

- [x] 2.1 `pkg/store/sqlite/queries.sql`: `InsertRestore` and
  `CreateStoreCheck` write `NULL`; the `CountStoreChecksEndedSince` comment
  says "has no end time". Put each edited block in its name-order place. Check:
  `make generate` runs, and `sqlc diff` is clean.
- [x] 2.2 `pkg/store/sqlite/backup.go`, `pkg/store/sqlite/restore.go`,
  `pkg/store/sqlite/restore_operation.go` and
  `pkg/store/sqlite/storechecks.go`: map the zero time to
  `sql.NullInt64{Valid: false}` and read `.Valid`. Add
  `optionalMillis(time.Time) sql.NullInt64`. Check: `go build ./...` and the
  tests of section 3 pass.
- [x] 2.3 Regenerate `pkg/store/sqlite/internal/db/queries.sql.go` with
  `make generate`. Check: `sqlc diff` is clean, and `git diff` holds no hand
  edit under `internal/db`.

## 3. Conformance

- [x] 3.1 Add `NullEndedWitness` to `pkg/store/storetest` and the case "a row
  that is not ended stores NULL, a row that is ended stores its time" to
  `RunBackup`, `RunRestore` and `RunStoreChecks`. Each adapter test implements
  the witness with one raw read. Check:
  `go test ./pkg/store/sqlite -run 'TestBackupConformance|TestRestoreConformance|TestStoreCheckConformance'`
  passes, and the PostgreSQL commands pass with a test database.
- [x] 3.2 Extend `testRestoreKeepsControlRows` in
  `pkg/store/storetest/restore.go` with a not-ended row and the witness. Check:
  `go test ./pkg/store/sqlite -run TestRestoreConformance` passes.
- [ ] 3.3 Apply each mutation of the "Check" section of `proposal.md` one at a
  time. Check: each named case fails and names the mutation.

## 4. Verification

- [ ] 4.1 `go test ./pkg/store/sqlite` passes. Check: the command exits 0.
- [ ] 4.2 The existing API tests of backups, restores and store checks pass.
  Check: `go test ./internal/api/server` exits 0.
- [ ] 4.3 `make check` reports zero findings. Check: the command exits 0.
