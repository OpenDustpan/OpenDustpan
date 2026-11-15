## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add Sessions conformance cases: other credential, ended session, limit, sweep, fence, Put and Get, in NEW file pkg/profile/profiletest/sessions.go (NEW TestSessions). Check: `go test ./pkg/profile/profiletest -run TestSessions -count=1` fails on main
- [ ] 1.2 Add table tests for merge, null removal, final, failed check, Expr and marks, in NEW file pkg/profile/open_test.go. Check: `go test ./pkg/profile/... -count=1`
- [ ] 1.3 Add a test that $neverStore on a plain value saves, in NEW file pkg/profile/document_check_test.go (NEW TestNeverStore). Check: `go test ./pkg/profile -run TestNeverStore -count=1`

## 2. Build

- [ ] 2.1 Implement NEW Service.Open in NEW file pkg/profile/open.go, the save fix in pkg/profile/document_check.go, mode refusal and provider refusal. Check: `go test ./pkg/profile/... -count=1`
- [ ] 2.2 Add NEW SQL adapters for the NEW Sessions port in both dialects, in NEW files pkg/store/sqlite/profile_sessions.go and pkg/store/postgres/profile_sessions.go, sealed with the root key, out of backups (pkg/backup/coverage.go:76), with Sweep. Check: `go test -timeout 60m ./pkg/store/... -count=1` and TestFrozenMigrations pass
- [ ] 2.3 Wire both NEW routes with actions and replay. The declaration file is the existing file internal/api/profile/routes.go, which this task extends with the two NEW routes. The adapter file is NEW file internal/api/profile/open.go. Check: `go test ./internal/api/profile/... -run TestRoutes -count=1` with %2F cases
- [ ] 2.4 Write the access audit entry for each open and refused open, with the NEW kind access in pkg/audit/audit.go. Check: `go test ./pkg/audit/... -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add the e2e profiles step in NEW file e2e/open_profile_serve_open_sessions_test.go. Existing route: POST /api/dustpan/v1/namespaces/{namespace}/profiles creates the profile. Existing credential kind: a personal token of a person. Then call the open route and the session read route, and replay the key. BLOCKED by the open route and the session read route until the Build section lands them. Check: `make e2e` runs the profiles step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Document open and sessions with errors and the one help command. Check: `make docs-lint` passes on docs/reference/profiles-api.md
- [ ] 4.3 Check the user path from the docs page alone. Check: A walk-through of the page opens and reads a session. Confirmed by: CI (an e2e step in the NEW file e2e/open_profile_serve_open_sessions_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.4 Run the full gate. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
