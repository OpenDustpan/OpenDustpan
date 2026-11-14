## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write a failing e2e for writer scope across two collections with the real Pulumi CLI and OpenTofu, route PUT /api/server/namespaces/{namespace}/collections/{collection}/grants, personal access token of a non-admin person Check: e2e/access_test.go new test fails on main
- [ ] 1.2 Write a failing e2e for viewer read and write with `pulumi stack export`, `tofu state pull`, `pulumi up` and `tofu apply`, grant route POST /api/server/namespaces/{namespace}/collections/{collection}/grants, personal access token of a viewer Check: e2e/access_test.go new test fails on main
- [ ] 1.3 Write a failing e2e for revocation, route DELETE /api/server/grants/{id}, and the retry with the same personal access token Check: e2e/access_test.go new test fails on main
- [ ] 1.4 Write a failing e2e for the access-review export, route GET /api/server/namespaces/{namespace}/collections/{collection}/access-review, with a site administrator session Check: e2e/access_review_test.go new test fails on main

## 2. Implementation

- [ ] 2.1 Fix each gap that the e2e steps find in the grant check, reached through the routes of the failing e2e steps (GET /api/server/namespaces/{namespace}/collections/{collection}/access-review, DELETE /api/server/grants/{id}) with a personal access token Check: `go test ./pkg/access/...`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring and routes keep their authorization actions Check: route test passes
- [ ] 4.2 Update docs/guides/roles-and-grants.md and docs/guides/access-review.md; each command on the pages uses the route GET /api/server/namespaces/{namespace}/collections/{collection}/access-review or DELETE /api/server/grants/{id} with a personal access token Check: the commands on the pages run in e2e
- [ ] 4.3 Usability check: the persona completes the path from the docs page alone Check: each error names one helping command
- [ ] 4.4 Run the full check Check: `make check` with zero findings
