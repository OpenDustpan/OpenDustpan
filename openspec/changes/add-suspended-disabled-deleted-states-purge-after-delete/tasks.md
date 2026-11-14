## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for batched purge in NEW internal/namespacesvc/purge_test.go that lists candidates with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token before purge Check: TestPurgeDeletedNamespacesAfter30Days in NEW internal/namespacesvc/purge_test.go
- [ ] 1.2 Add failing server test for retention in NEW internal/namespacesvc/purge_test.go that reads state with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token Check: TestKeepDeletedNamespaceBefore30Days in NEW internal/namespacesvc/purge_test.go

## 2. More failing tests

- [ ] 2.1 Add failing server test for restore-wins recheck in NEW internal/namespacesvc/purge_test.go that drives restore with ANY /api/inventory/deleted-units/{}/{}/restore with access token Check: TestPurgeRechecksStateInTransaction in NEW internal/namespacesvc/purge_test.go
- [ ] 2.2 Add failing server test for purge audit in NEW internal/namespacesvc/purge_test.go that reads the audit with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token Check: TestPurgeWritesAuditEntry in NEW internal/namespacesvc/purge_test.go

## 3. Implement

- [ ] 3.1 Implement bounded batched purge in NEW internal/namespacesvc/purge.go with per-namespace commit, stored cursor and single-runner fencing Check: `go test ./internal/namespacesvc -run TestPurge -count=1` passes

## 4. Side effects (computed from main)

- [ ] 4.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 5. Definition of done

- [ ] 5.1 Add docs page for the full namespace lifecycle with purge timing and preview list in NEW docs/guides/namespace-lifecycle.md Check: Docs page for namespace lifecycle renders
- [ ] 5.2 Wire the scheduled purge task and run the e2e preview step with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token then complete the usability check from the docs page alone Check: E2E step lists deleted units with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token and `make check` reports zero findings
