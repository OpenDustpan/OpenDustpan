## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the conformance cases of the RuleStore port to the existing suite pkg/jobproxy/jobproxytest/jobproxytest.go (func RunRuleStore, :49): a namespace scope and a unit scope, the version check (ErrConflict on a stale version, 0 for a scope with no set), Delete, the fence, and an invalid document (jobproxy.ErrInvalid). Run the suite from the NEW files pkg/store/sqlite/proxyrules_test.go and pkg/store/postgres/proxyrules_test.go. Check: `go test -timeout 60m ./pkg/store/... -run ProxyRule -count=1` fails on main (no adapter)
- [ ] 1.2 Add failing service tests for the join order (namespace, then unit; jobproxy.Join at pkg/jobproxy/join.go:16), the digest (rulestore.DocumentDigest, pkg/jobproxy/rulestore/rulestore.go:67) in the audit entry with the old and the new digest, an empty set, and the rendered view (jobproxy.Render, pkg/jobproxy/render.go:188), in the NEW file internal/jobproxysvc/jobproxysvc_test.go. Check: `TestJoinNamespaceAndUnitProxyRuleSets` fails on main
- [ ] 1.3 Add a failing service test for If-Match: a stale version answers 412 and changes nothing; no If-Match saves (the provisional decision of the proposal). In the NEW file internal/jobproxysvc/ifmatch_test.go. Check: `TestProxyRuleSetStaleVersionRefused` fails on main
- [ ] 1.4 Add failing handler tests for the grant, the fresh authentication, the size limit, the content type and the rendered access, one hostile table for each layer, in the NEW file internal/api/server/proxyrules_test.go. Check: `TestProxyRulesHandlerRefusals` fails on main

## 2. Build

- [ ] 2.1 Add the store adapters of `rulestore.RuleStore` (pkg/jobproxy/rulestore/rulestore.go:100) in the NEW files pkg/store/sqlite/proxyrules.go and pkg/store/postgres/proxyrules.go, with the NEW table proxy_rule_sets (scope kind, namespace ID, unit ID or empty, document, digest, version, updated_at_ms BIGINT) edited in place in name order in pkg/store/sqlite/migrations/0168_baseline.sql and pkg/store/postgres/migrations/0168_baseline.sql and their .sql.sha256 files, the queries in name order in pkg/store/sqlite/queries.sql and pkg/store/postgres/queries.sql, and the table in the portable backup column list pkg/backup/portable_tables.go. The version check and the audit entry run in the transaction of the write. Run `make generate` for the sqlc code in pkg/store/sqlite/internal/db and pkg/store/postgres/internal/db. Check: the suite of task 1.1 passes on SQLite and PostgreSQL, and `sqlc diff` is clean
- [ ] 2.2 Add the NEW service package internal/jobproxysvc: Get and Put of the namespace and the unit sets through the port, the join and the rendered view through pkg/jobproxy, the audit entry with the old and the new digest. Add the NEW actions `namespace:manage-proxy-rules` and `unit:manage-proxy-rules` to pkg/identity/identity.go. Check: `go test ./internal/jobproxysvc/... ./pkg/jobproxy/...` passes
- [ ] 2.3 Add the routes of ADR 330 decision 12: GET and PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules (namespace:manage-proxy-rules for PUT), GET and PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules (unit:manage-proxy-rules for PUT), and GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules/rendered (unit read access). Declare them in the route table in internal/api/server/routes.go, join their apidesc labels in internal/api/server/operations.go (joinLabels), and serve them in the NEW file internal/api/server/proxyrules.go; wire the service in pkg/dustpan. Run `make generate` and `make clients-generate` for internal/api/apidesc/openapi.json and the clients; never edit them by hand. Check: `go test ./internal/api/server/... -run ProxyRules -count=1` passes, and the route test lists the actions

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add the e2e step in the NEW file e2e/proxy_rules_test.go against the real binary over TLS: PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules and GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules/rendered (both added by this change) with a personal access token of a namespace administrator. Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the NEW guide docs/guides/job-proxy-rules.md in the same pull request: each route, each field, each error with its one command, and the debug-only note of the rendered route. Check: `make docs-lint` passes
- [ ] 4.3 Run the usability check Check: a namespace administrator completes the path from docs/guides/job-proxy-rules.md alone. A person's step.
- [ ] 4.4 Run `make check` with zero findings Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
