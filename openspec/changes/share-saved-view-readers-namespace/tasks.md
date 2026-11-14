## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing domain tests in pkg/savedview/savedviewtest for visibility, collection ID, unshare, replace with an invalid query and old rows. Check: TestViewShareNamespace and TestViewRowFailsValidateOnRead fail on main
- [ ] 1.2 Write failing handler tests for internal/api/inventory/views.go: share, outsider 404, 403 without view:share, stale If-Match 412, delete by a namespace administrator, using an access token. Check: TestViewUnitsPerReader fails on main

## 2. Build

- [ ] 2.1 Add visibility to pkg/savedview/savedview.go and the columns in pkg/store/postgres/savedviews.go and the sqlite baseline. Check: `go test -race ./pkg/savedview/...` passes
- [ ] 2.2 Add the visibility route and the replace route in internal/api/inventory/views.go with view:share, the fence, Idempotency-Key, If-Match and audit entries. Add the collection-delete reset to private in pkg/store/postgres/savedviews.go. Check: `go test -race ./internal/api/inventory/...` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step in e2e/saved_views.go that calls POST /api/dustpan/v1/namespaces/{namespace}/views and the NEW PUT visibility route with an access token (gate: view:create and view:share), then GET the view units route as a second principal with its own access token. Check: `make e2e` passes the saved view step
- [ ] 4.2 Add the docs page for saved views and run the usability check from the page alone. Check: The docs commands run as written in a test
- [ ] 4.3 Run the full checks. Check: `make check` reports zero findings
