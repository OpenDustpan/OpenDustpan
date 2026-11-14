## 1. Schema

- [x] 1.1 Confirm the ID column lines of `proposal.md` on the checkout. Check:
  each `path:line` quotes the line that it names.
- [x] 1.2 Edit `pkg/store/sqlite/migrations/0168_baseline.sql`: remove
  `DEFAULT ''` from `namespaces.id` (`:23`), `stacks.id` (`:33`) and
  `blob_purges.namespace_id` (`:333`). Keep the other sixteen ID columns with
  their default, because PostgreSQL has the same default. Add no numbered
  migration. Check: `rg -n "id TEXT NOT NULL DEFAULT ''|namespace_id TEXT NOT NULL DEFAULT ''"`
  lists only the columns that stay, and `git diff` holds one file.
- [x] 1.3 Write the new SHA-256 in
  `pkg/store/sqlite/migrations/0168_baseline.sql.sha256`. Check:
  `sha256sum pkg/store/sqlite/migrations/0168_baseline.sql` matches the file.
- [x] 1.4 Regenerate sqlc with `make generate`. Check: `sqlc diff` is clean and
  `git diff` holds no hand edit under `internal/db`.

## 2. Store test

- [x] 2.1 Add `TestInsertWithoutIDIsRefused` to
  `pkg/store/sqlite/sqlite_test.go` and
  `pkg/store/postgres/postgres_test.go`. The table has one case for
  `namespaces`, `stacks` and `blob_purges`. Each case inserts a row with no ID
  column and expects an error. Check: both tests pass.
- [x] 2.2 Apply each mutation of the "Check" section of `proposal.md` one at a
  time. Check: the named case fails and names the table and the mutation.

## 3. Verification

- [x] 3.1 The existing store conformance tests pass. Check:
  `go test ./pkg/store/sqlite` passes, and the PostgreSQL command passes with a
  test database.
- [x] 3.2 `make check` reports zero findings. Check: the command exits 0.
