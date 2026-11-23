## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main. Read the code of `housekeeping-principals-install` on main too: `CreateNamespaceWithGrants` in `internal/store/sqlite/access.go` and `internal/store/postgres/access.go`, and `housekeepingNamespaceGrants` in `pkg/access/service.go`. Record each difference in the pull request body. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 SECURITY. Stage tests, one table of the hostile rows of the design document for each dialect: NEW `internal/store/sqlite/backup_housekeeping_principals_internal_test.go` and NEW `internal/store/postgres/backup_housekeeping_principals_internal_test.go`, each with `t.Parallel()`. Each case stages a backup that holds the row, and the stage returns `backup.ErrCorrupt`. One case stages an unchanged export and passes. Check: `go test ./internal/store/sqlite -run TestCheckStagedHousekeepingPrincipals -count=1` fails on main, because NEW `checkStagedHousekeepingPrincipals` does not exist (`internal/store/sqlite/backup.go:954` calls no such check).
- [ ] 1.2 SECURITY. Point-of-use tests: NEW conformance cases in `pkg/access/accesstest` that change a seeded row (kind `person`) and delete a seeded row, then create a namespace. Each case expects `access.ErrInvalid`, no namespace row and no grant. Run by `TestAccessConformance` on both dialects. A service test in NEW `pkg/access/housekeeping_integrity_test.go` expects one entry with the outcome `denied` and the ID of the principal. Check: `go test ./pkg/access -run TestCreateNamespaceChangedHousekeepingPrincipal -count=1` fails on main, because NEW `IsBuiltinHousekeeping` does not exist.
- [ ] 1.3 SECURITY. Reserved subject tests: NEW `pkg/principal/housekeeping_reserved_test.go` (`CreatePrincipal` with `housekeeping:x`, `DeletePrincipal` of a `builtin` principal, and a revoke of its grant that still works), and a handler case in `internal/api/server/principals_test.go` that expects the existing answer for `reserved subject`. Check: the tests fail on main, because `pkg/principal/service.go:267` (`DeletePrincipal`) refuses only the namespace service subject.
- [ ] 1.4 Conformance case "a grant with no ID is ErrInvalid" for `CreateNamespaceWithGrants` in `pkg/access/accesstest/accesstest.go`. Check: after the case is in, removing the guard `grants[i].ID == ""` in the SQLite store fails `TestAccessConformance`.

## 2. Build

- [ ] 2.1 Add NEW `principal.IsBuiltinHousekeeping` in `pkg/principal/housekeeping.go` (design decision 1). Check: the unit test of the function passes for each purpose and fails for each changed field.
- [ ] 2.2 Add `checkStagedHousekeepingPrincipals` to `internal/store/sqlite/backup_housekeeping_principals.go` and `internal/store/postgres/backup_housekeeping_principals.go` (NEW files), and call it in `checkStagedRows` of each dialect (`internal/store/sqlite/backup.go`, `internal/store/postgres/backup.go`). Add the defense-in-depth comment. Check: 1.1 passes.
- [ ] 2.3 Read and check the principal row of each housekeeping grant in `CreateNamespaceWithGrants` of both dialects, in the same transaction, and write the denied audit entry in `Service.CreateNamespace` (`pkg/access/service.go`). Add the defense-in-depth comment. Check: 1.2 passes on both dialects.
- [ ] 2.4 Reserve the prefix `housekeeping:` and refuse the delete of a `builtin` principal (design decisions 5 and 6) in `pkg/principal/principal.go` and `pkg/principal/service.go`. Check: 1.3 passes.
- [ ] 2.5 Add one row to the table of restore errors in `docs/guides/backup-restore-upgrade.md`. Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate the generated files that the change touches with `make generate`. Never edit a generated file by hand. Check: `make generate` leaves no diff. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 E2e: `POST /api/dustpan/v1/restores` with the bootstrap admin token, for a backup in which a housekeeping row is changed, in `e2e/backup_cross_test.go`, expects the refusal `backup data is corrupt`. Check: `make e2e` passes.
- [ ] 4.2 Full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
