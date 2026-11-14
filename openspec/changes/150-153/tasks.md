## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Failing server test for the history list in pkg/history/history_test.go (existing test file of pkg/history). Check: TestHistoryListReturnsOrderedItems; BLOCKED by the NEW history route; access token
- [ ] 1.2 Failing server test for blame, including a redacted item, in pkg/history/history_test.go. Check: TestBlameReturnsLastChanger; BLOCKED by the NEW blame route; access token
- [ ] 1.3 Failing server test for query at with the edges before the first item and after delete, in pkg/history/history_test.go. Check: TestPointInTimeReturnsStateAt; BLOCKED by the NEW query at; access token
- [ ] 1.4 Failing server test for Redact with the 409, 403, 400 and 404 cases, in pkg/history/purge_test.go. Check: TestRedactKeepsMetadata; BLOCKED by the NEW redact route; access token
- [ ] 1.5 Failing server test for retention, the repair pass and the Restore window, in pkg/history/retention_test.go. Check: TestRetentionDeletesOldArtifacts; calls ANY /api/server/deleted-units/sweep with an access token
- [ ] 1.6 Failing server test for the fail-closed read, the rate limit and the page limit, in pkg/history/leak_test.go. Check: TestHistoryUnavailableFailsClosed; BLOCKED by the NEW history route; access token

## 2. build

- [ ] 2.1 Implement the sqlite and postgres adapters with the fence and data-key encryption, in NEW pkg/store/sqlite/history.go and NEW pkg/store/postgres/history.go. Check: go test ./pkg/store/... -run History -timeout 60m
- [ ] 2.2 Wire the NEW history, blame, point-in-time and redact routes with the NEW actions. The declaration file is NEW internal/api/apidesc/history_routes.go and the adapter file is NEW pkg/dustpan/history_routes.go. Check: route test lists each action; the NEW history route returns 200 with an access token
- [ ] 2.3 Wire artifact expiry and Restore to the existing schedules and restore routes, in pkg/dustpan/schedules.go and pkg/workspace/service.go:1771 (func Restore). Check: the existing ANY /api/inventory/deleted-units/{}/{}/restore returns success inside 30 days with an access token
- [ ] 2.4 Add the CLI history read and redact steps in NEW cmd/dustpan/history.go. Check: CLI redact with reason secret-exposure prints the redaction record

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. definition of done

- [ ] 4.1 E2E step against the real CLI for history after an update Check: make e2e step; BLOCKED by the NEW history route until it ships. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Docs page for history, blame, point-in-time, redaction and retention Check: docs page in the same pull request; make docs-lint
- [ ] 4.3 Usability check Check: a developer completes the path from the docs page alone. A person's step.
- [ ] 4.4 Full checks Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
