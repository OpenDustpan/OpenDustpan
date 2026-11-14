## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test of the failed-export audit in NEW file pkg/access/review_collection_audit_test.go. Check: `go test ./pkg/access -run TestCollectionReviewFailedExportWritesAudit` fails on main.
- [ ] 1.2 Write the failing test of the snapshot in NEW file pkg/access/review_collection_snapshot_test.go. Check: `go test ./pkg/access -run TestCollectionReviewPagesFromSnapshot` fails on main.
- [ ] 1.3 Write the failing test of the cap, with the cases 2000, 2001 and the default collection over the cap, in NEW file pkg/access/review_collection_cap_test.go. Check: `go test ./pkg/access -run TestCollectionReviewCap` fails on main.

## 2. Fix

- [ ] 2.1 Write the audit entry on failure and build the snapshot and the cap in pkg/access/review_service.go. Check: The three tests pass.
- [ ] 2.2 Use the existing code `access.collection-unit-cap` from internal/errcode/catalog_access.go for the cap answer, with the text that names the namespace review route. Check: `go test ./internal/errcode` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step that calls the route ANY /api/server/namespaces/{}/collections/{}/access-review with an access token of a site administrator, through the read-right gate on the collection, for a missing collection and for a paged export. Confirmed by: CI (make e2e). Check: `make e2e` passes the step against the real server.
- [ ] 4.2 Add the docs page of the export, with the cap error, the namespace review route as the next command, and run the usability check from the page alone. Confirmed by: CI (make docs-lint). Check: `make docs-lint` has zero findings.
- [ ] 4.3 Run the full check. Confirmed by: CI (make check). Check: `make check` has zero findings.
