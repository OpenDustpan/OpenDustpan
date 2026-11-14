## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add a failing test for the coded blobs-missing failure through sanitizeRestoreError in pkg/backup/restore_service.go. Check: TestRestoreFailedReturnsCatalogCode in pkg/backup fails on main; calls `ANY /api/server/restores/{}` after the restore grant with access token.
- [ ] 1.2 Add a failing test for an unknown backup ID refused at start with the NEW error code `backup.not-found`. Check: TestRestoreUnknownBackupRefused in internal/api/server fails on main; calls `ANY /api/server/restores` after the restore grant with access token.

## 2. Build

- [ ] 2.1 Add the NEW catalog entry `backup.not-found` in internal/errcode/catalog_backup.go. Check: Catalog unit test for `backup.not-found` passes in internal/errcode.
- [ ] 2.2 Store the catalog code beside the failure text in the restore row. Edit the baseline migration in pkg/store/sqlite/migrations/ and pkg/store/postgres/migrations/ in place, with no numbered migration. Check: Restore row round-trip test passes in each dialect.
- [ ] 2.3 Add the start-time backup existence check after the grant check in internal/api/server/restores.go. Check: Start handler test passes for unknown and malformed backup IDs through `ANY /api/server/restores` after the restore grant with access token.
- [ ] 2.4 Return the errorCode member on GET by editing internal/api/server/restores.go:447 (func errorCodeOf) and internal/api/apidesc/openapi.json:53, update the gate list, and regenerate the clients. Confirmed by: CI (make clients-check). Check: Gate list test passes for the new member on `ANY /api/server/restores/{}` after the restore grant with access token; make clients-check is clean.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire `ANY /api/server/restores` with its authorization actions, an e2e step against the real CLI, the docs page for errorCode and the unknown backup behavior, and the usability check of the Operator path. Confirmed by: CI (make check). Check: E2E step calls `ANY /api/server/restores` and `ANY /api/server/restores/{}` after the restore grant with access token; docs page updated; `make check` shows zero findings.
