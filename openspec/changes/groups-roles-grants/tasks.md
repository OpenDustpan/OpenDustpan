## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 Write a failing e2e for writer scope across two collections with the real Pulumi CLI and OpenTofu, route PUT /api/server/namespaces/{namespace}/collections/{collection}/grants, personal access token of a non-admin person Check: e2e/access_test.go new test fails on main
- [x] 1.2 Write a failing e2e for viewer read and write with `pulumi stack export`, `tofu state pull`, `pulumi up` and `tofu apply`, grant route POST /api/server/namespaces/{namespace}/collections/{collection}/grants, personal access token of a viewer Check: e2e/access_test.go new test fails on main
- [x] 1.3 Write a failing e2e for revocation, route DELETE /api/server/grants/{id}, and the retry with the same personal access token Check: e2e/access_test.go new test fails on main
- [x] 1.4 Write a failing e2e for the access-review export, route GET /api/server/namespaces/{namespace}/collections/{collection}/access-review, with a site administrator session Check: e2e/access_review_test.go new test fails on main

## 2. Implementation

- [ ] 2.1 Fix each gap that the e2e steps find in the grant check in pkg/access/access.go (the reach function; confirm the file name on main in task 0.1), reached through the routes of the failing e2e steps (GET /api/server/namespaces/{namespace}/collections/{collection}/access-review, DELETE /api/server/grants/{id}) with a personal access token Check: `go test ./pkg/access/...`

- [ ] 2.2 Let the TFE discovery routes answer a holder of a grant inside the namespace. Edit the declarations in internal/api/tfe/routes.go (the routes at :126, :143, :164) and the adapter in internal/api/tfe/tfe.go. Check: `go test ./internal/api/tfe/...`
- [ ] 2.3 Let the Pulumi organization routes answer a holder of a grant inside the namespace. Edit the declarations in internal/api/pulumi/routes.go (for example /api/user/organizations/default at :156). Check: `go test ./internal/api/pulumi/...`
- [ ] 2.4 Write hostile tests in pkg/dustpan/route_actions_test.go. Test one: a collection grant never shows the units of another collection. Test two: a principal with no grant inside the namespace gets the 404 with the same body as an unknown namespace. Test three: a revoked grant loses discovery on the next request. Check: `go test ./pkg/dustpan/...`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring and routes keep their authorization actions Check: route test passes
- [x] 4.2 Update docs/guides/roles-and-grants.md and docs/guides/access-review.md; each command on the pages uses the route GET /api/server/namespaces/{namespace}/collections/{collection}/access-review or DELETE /api/server/grants/{id} with a personal access token Check: the commands on the pages run in e2e
- [ ] 4.3 Usability check: the persona completes the path from the docs page alone Check: each error names one helping command
- [x] 4.5 Run the e2e tests of tasks 1.1 to 1.3 in the existing file e2e/access_test.go and of task 1.4 in the existing file e2e/access_review_test.go. They use the real Pulumi CLI and OpenTofu, the route DELETE /api/server/grants/{id} and a personal access token of a non-admin person. Check: `go test ./e2e/ -run Access`
- [x] 4.6 Run the e2e test in the new file e2e/collection_grants_test.go (NEW; it comes with a separate pull request) with the real OpenTofu CLI, the route GET /api/v2/organizations/{organization}/entitlement-set and a personal access token of a person who holds a grant on one collection only. Check: the NEW test TestCollectionGrants passes
- [x] 4.4 Run the full check Check: `make check` with zero findings
