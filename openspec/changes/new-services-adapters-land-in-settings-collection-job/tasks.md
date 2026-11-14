## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the new file internal/settingsvc/boundary_test.go with a test that pkg/setting, pkg/collection and pkg/job export no service constructor. Check: go test ./internal/settingsvc/... fails on main. Confirmed by: CI (go test).
- [ ] 1.2 Add the new file internal/collectionsvc/default_collection_test.go with a test that a new namespace has the default collection and the catch-all placement rule after create. Check: go test ./internal/collectionsvc/... fails on main. Confirmed by: CI (go test).

## 2. Move

- [ ] 2.1 Move pkg/collection/placement_service.go to internal/collectionsvc, the settings service to internal/settingsvc, and the reconciler with pkg/job/reconcile_test.go to internal/jobsvc. Check: go test ./internal/settingsvc/... ./internal/collectionsvc/... ./internal/jobsvc/... Confirmed by: CI (go test).

## 3. Definition of done

- [ ] 3.1 Wire the services in pkg/dustpan/settings_resource.go. Run the e2e step that calls GET /api/dustpan/v1/namespaces/{namespace}/settings with an access token. Update the SDK docs page. Check the usability of the Embedder path from the docs page. Check: make e2e and make check with zero findings. Confirmed by: CI (make check).
