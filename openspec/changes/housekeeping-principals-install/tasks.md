## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main. Record each difference in the pull request body. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Conformance cases for the install principals and grants, on both dialects: add NEW cases to the case list of `principaltest.Run` (`pkg/principal/principaltest/principaltest.go:30`), which `TestPrincipalConformance` runs in `pkg/store/sqlite/sqlite_test.go:208` and `pkg/store/postgres/postgres_test.go:278`: one principal of kind `service` and source `builtin` for each purpose, each with stored grants that have a creator and a time, and the `source` filter with its total and its cursor. Check: `go test ./pkg/store/sqlite -run TestPrincipalConformance -count=1 -timeout 60m` fails on main, because the case list has no case for the install principals and the store writes no `builtin` principal.
- [ ] 1.2 The list route filters by source. Test: NEW `TestListPrincipalsFiltersBySource` in `internal/api/server/principals_test.go`, next to `TestListPrincipalsFiltersByKind`. Check: the test fails on main: no `source` filter.

## 2. Build

- [ ] 2.1 In the NEW file `pkg/principal/housekeeping.go`, add NEW `HousekeepingPurpose`, NEW `HousekeepingPurposes()` and NEW `principal.Housekeeping`, and the `builtin` source to the source constants (`pkg/principal/principal.go:116`) and to `validateSource` (`pkg/principal/principal.go:629`). Write the install principals and their stored grants in the baseline of each dialect (ADR 322: `pkg/store/sqlite/migrations/0168_baseline.sql` and `pkg/store/postgres/migrations/0168_baseline.sql`, in the name order of the file). Check: 1.1 passes on SQLite and PostgreSQL.
- [ ] 2.2 Add the `source` filter to the existing list in `internal/api/server/principals.go:730` (and `ListPrincipals` in `pkg/principal/service.go:201`, with its callers `pkg/dustpan/kind.go:153`, `internal/localusersvc/service_test.go:835`, `internal/api/server/principals_test.go:999`, `pkg/principal/principal_test.go`, `pkg/principal/demoperson_test.go` and `pkg/principal/devsubject_test.go`), with the label of the filter in `internal/api/server/operations.go` (joinLabels). The store filters, not the service: filtering after the read breaks the page totals and the cursors. Edit these files too: `pkg/principal/store.go` (the port: a `source` argument of the list and the count methods), `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql` (the `source` condition in `ListPrincipals` and `CountPrincipals`, in the name order of the file), `pkg/store/sqlite/principals.go`, `pkg/store/sqlite/principals_namespace.go`, `pkg/store/postgres/principals.go` and `pkg/store/postgres/principals_namespace.go`, the other callers of the Store method (`pkg/store/sqlite/reach.go:22`, `pkg/store/postgres/reach.go:22`, `pkg/principal/principaltest/namespace.go:138`, `pkg/principal/principaltest/bench.go` and the calls in `pkg/principal/principaltest/principaltest.go`), and every other caller and fake that `go build ./... && go vet ./...` names. Check: 1.2 passes, and `go test ./pkg/principal/... -count=1` passes.
- [ ] 2.3 Update `GLOSSARY.md` (file on main; path `GLOSSARY.md`): add "Housekeeping principal". Do not remove "Namespace service principal": the first trigger change removes it. Add the `builtin` value to the `source` row of the principal fields in `docs/guides/principals-and-groups.md` (line 105). Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate the API description `internal/api/apidesc/openapi.json` with `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`, then run `make generate`: it regenerates the sqlc code in `pkg/store/sqlite/internal/db` and `pkg/store/postgres/internal/db` and the TypeScript client in `clients/typescript`. Never edit a generated file by hand. No new route. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 E2e: use an administrator access token. List the principals with the existing `GET /api/server/principals?kind=service&source=builtin`, and read the grants with the existing `GET /api/server/principals/{principal}/grants`, in `e2e/principals_test.go`. Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Usability check: an operator finds the built-in principals and their grants from the page `docs/guides/principals-and-groups.md` alone. Check: the check is recorded in the pull request. A person's step.
- [ ] 4.3 Full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
