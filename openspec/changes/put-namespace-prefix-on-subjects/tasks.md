## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Tests

- [ ] 1.1 Write failing tests for the prefixed subject, the allowlist, the 256 character bound and the reserved prefix in NEW pkg/principal/namespaced_test.go. Check: go test ./pkg/principal -run TestNamespacedSubject fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the create route POST /api/server/namespaces/{namespace}/principals to the NEW constructor NewNamespacedSubject in its handler with gate namespace:manage-principals and an access token. Confirmed by: CI (go test ./internal/servertest) Check: POST /api/server/namespaces/{namespace}/principals with an access token under gate namespace:manage-principals succeeds for ci-deploy in acme and beta and a duplicate answers 409
- [ ] 3.2 Write the docs page NEW docs/guides/namespace-principals.md and add an e2e step for POST /api/server/namespaces/{namespace}/principals with an access token under gate namespace:manage-principals. A person's step. Check: POST /api/server/namespaces/{namespace}/principals with an access token under gate namespace:manage-principals shows the stored subject and NAMESPACE:acme/x answers 400 in make e2e
