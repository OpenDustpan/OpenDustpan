## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for Pulumi placement and refusal in NEW file internal/api/pulumi/placement_test.go Check: `go test ./internal/api/pulumi -run TestPlacement -count=1` fails on main (NEW tests)
- [ ] 1.2 Write failing tests for TFE project and workspace-name rule placement and refusal Check: NEW test `TestWorkspaceProjectPlacement` in internal/api/tfe fails on main
- [ ] 1.3 Write failing tests for namespace create: default, catch-all, audit, opt-out, rollback on failure in NEW file internal/api/tfe/namespace_default_placement_test.go Check: NEW test `TestNamespaceDefaultPlacement` fails on main
- [ ] 1.4 Write failing tests for pattern limits, 100-rule limit, rule in another namespace, stale rule, list order, update and reorder in NEW file internal/api/tfe/placement_resolver_test.go Check: NEW test `TestPlacementResolver` fails on main
- [ ] 1.5 Write failing tests for concurrent same-name creates, a reorder racing a create and a collection delete racing a create in NEW file internal/api/tfe/placement_concurrent_test.go Check: NEW test `TestPlacementConcurrent` fails on main

## 2. Implementation

- [ ] 2.1 Implement the resolver in NEW file internal/api/tfe/placement_resolver.go and wire it into the Pulumi and TFE create paths (internal/api/pulumi, internal/api/tfe) Check: NEW `TestPlacementResolver` passes
- [ ] 2.2 Add the opt-out field to the existing namespace create route, in its declaration file and its adapter file in internal/api/tfe, and to internal/api/apidesc/openapi.json, and store the default in the create transaction Check: NEW `TestNamespaceDefaultPlacement` passes
- [ ] 2.3 Add the foreign keys to the baseline migration of each dialect, in the store packages pkg/store/sqlite and pkg/store/postgres Check: `make check` passes and the store conformance suite refuses a delete of a named collection. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 2.4 Write the audit entries for placements, rule changes and refusals in NEW file internal/api/tfe/placement_audit.go Check: NEW test `TestPlacementAudit` passes
- [ ] 2.5 Add e2e steps in e2e/placement_test.go. With an admin access token, call `POST /api/dustpan/v1/namespaces/{namespace}/collections` and the namespace create route with and without the opt-out. Then run `pulumi stack init` and `tofu init` with a developer access token, including the TFE project path Check: `make e2e` runs the steps with the real CLIs on SQLite and PostgreSQL. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Check that the placement rule, collection and namespace create routes declare actions Check: the route policy test passes
- [ ] 4.2 Update the collections and placement page with the default, the opt-out, the grammar and the TFE project steps Check: each command on the page runs as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/collections_placement_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.3 Do the usability check from the docs page alone and run all checks Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
