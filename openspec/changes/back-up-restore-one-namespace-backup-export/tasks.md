## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write NEW `pkg/backup/namespace_export_test.go` with service and store tests for namespace-scoped portable export with the stable namespace ID. Check: `NEW pkg/backup/namespace_export_test.go` TestNamespaceBackupExportWritesPortableArchive fails on main, passes after the change.
- [ ] 1.2 Write NEW `internal/api/server/namespace_backup_test.go` with route tests for the NEW export route, the NEW actions, and the audit entry. Check: `NEW internal/api/server/namespace_backup_test.go` TestNamespaceBackupExportRouteAuth fails on main, passes after the change.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 BLOCKED by NEW route GET /api/dustpan/v1/namespaces/{namespace}/backup/export: wire the NEW export route with its authorization actions and audit entries. Check: BLOCKED by NEW route GET /api/dustpan/v1/namespaces/{namespace}/backup/export; no existing route serves a namespace export.
- [ ] 3.2 Write NEW `docs/reference/namespace-backup.md` for the namespace export path, grants, errors, and the 512 MiB limit. Check: `NEW docs/reference/namespace-backup.md` each command runs as written against a dev server.
- [ ] 3.3 Run usability check from the docs page alone and `make check` with zero findings. Check: `make check` with zero findings after the docs-page usability check.
