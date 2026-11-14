## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)` and NEW `internal/api/server/namespace_restore_test.go` that backs up A, changes A and B, restores A; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore` and its gate. Check: `TestNamespaceRestoreOnlyTargetNamespace` fails on main in NEW `internal/api/server/namespace_restore_test.go`.
- [ ] 1.2 Add failing tests in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)` and NEW `internal/api/server/namespace_restore_test.go` for wrong-scope backup, missing blob, unopenable key, and concurrent isolation; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore` and its gate. Check: `TestNamespaceRestoreChecks` fails on main in NEW `internal/api/server/namespace_restore_test.go`.

## 2. Implementation

- [ ] 2.1 Add the NEW restore route in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)` and NEW `internal/api/server/namespace_restore.go` with namespace grant, writer fence, tombstone replay, safety backup, and audit entries. Check: `POST /api/dustpan/v1/namespaces/{namespace}/restore` with access-token credential answers 202 in `internal/api/server/namespace_restore.go`.
- [ ] 2.2 Add user docs page in NEW `docs/guides/namespace-backup-restore.md` for namespace backup and restore including restore under another key. Check: NEW `docs/guides/namespace-backup-restore.md` renders and `make docs-lint` passes for changed files.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the route in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)`, run e2e against the real CLI with an access token through NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore`; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore` and its gate. Check: E2e restores namespace A against the real CLI with an access token while B keeps changes; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore`.
- [ ] 4.2 Run full checks with zero findings. Check: `make check` reports zero findings.
