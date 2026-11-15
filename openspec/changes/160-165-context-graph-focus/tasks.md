## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for GET unit context by stable ID with provenance in NEW file internal/api/inventory/context_routes_test.go. Check: TestContextReadByStableID on route GET /api/dustpan/v1/namespaces/{}/units/{}/context with access token and gate unit:read fails on main.
- [ ] 1.2 Add failing server test for GET unit focus with neighbor bounds and folds in NEW file internal/api/inventory/focus_routes_test.go. Check: BLOCKED by NEW focus route and EdgeReader adapter; test will call NEW GET focus route with access token and gate unit:read once wired.

## 2. Build

- [ ] 2.1 Add object_context and object_context_owners tables and SQL adapters through existing ports in NEW file internal/api/inventory/context_store.go. Check: sqlc diff clean and context conformance passes on sqlite and postgres for object_context and object_context_owners.
- [ ] 2.2 Add graph EdgeReader adapter (port graph.EdgeReader at pkg/graph/graph.go:183) over dependency edges with indexes and conformance in NEW file internal/api/inventory/edge_reader.go. Check: dependency.Store.Edges adapter plus SQL source and dependent indexes and dependencytest window limit and cross-namespace cases pass.
- [ ] 2.3 Wire context and NEW focus routes with authorization and limits. Declare the routes in the route table of the adapter package (`make generate` then writes internal/api/apidesc/openapi.json; never edit it by hand) and write the adapter in NEW file internal/api/inventory/context_handlers.go. Check: Route tests assert GET PATCH DELETE context with namespace:read and unit:read and NEW focus route with unit:read and 404 and limit behavior.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add e2e step for context read and focus on one stack and one workspace in NEW file e2e/context_focus_test.go. Check: BLOCKED by NEW focus route; e2e will call GET /api/dustpan/v1/namespaces/{}/units/{}/context with personal token plus NEW focus route once wired. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write docs page for context and focus in NEW file docs/guides/context-and-focus.md. Check: Docs page shows context and focus calls with exact output.
- [ ] 4.3 Run usability check from docs page alone plus full checks. Check: `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
