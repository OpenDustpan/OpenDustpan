## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add Sessions conformance cases: other credential, ended session, limit, sweep, fence. Check: `go test ./pkg/profile/profiletest -run TestSessions -count=1` fails on main
- [ ] 1.2 Add table tests for merge, null removal, final, failed check, Expr and marks. Check: `go test ./pkg/profile/... -count=1`
- [ ] 1.3 Add a test that $neverStore on a plain value saves. Check: `go test ./pkg/profile -run TestNeverStore -count=1`

## 2. Build

- [ ] 2.1 Implement Service.Open, the save fix in document_check.go, mode refusal and provider refusal. Check: `go test ./pkg/profile/... -count=1`
- [ ] 2.2 Add SQL adapters for Sessions in both dialects, sealed with the root key, out of backups, with Sweep. Check: `go test ./pkg/store/... -count=1` and TestFrozenMigrations pass
- [ ] 2.3 Wire both routes with actions and replay. Check: `go test ./internal/api/profile/... -run TestRoutes -count=1` with %2F cases
- [ ] 2.4 Write the access audit entry for each open and refused open. Check: `go test ./pkg/audit/... -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add the e2e profiles step: open, read the session, replay the key. Check: `make e2e` runs the profiles step
- [ ] 4.2 Document open and sessions with errors and the one help command. Check: `make docs-lint` passes on docs/reference/profiles-api.md
- [ ] 4.3 Check the user path from the docs page alone. Check: A walk-through of the page opens and reads a session
- [ ] 4.4 Run the full gate. Check: `make check` reports zero findings
