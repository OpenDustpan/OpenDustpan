## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add Upstream idempotency and ErrVersionMismatch conformance cases Check: pkg/trigger/triggertest Upstream cases for applied, duplicate, older and concurrent older version fail on main
- [ ] 1.2 Extend save conformance cases for cycle and follower count Check: pkg/trigger/triggertest save cases fail on main
- [ ] 1.3 Add saver read-check cases Check: pkg/outputshare conformance cases for missing, deleted, disabled and grant-lost saver fail on main

## 2. Implementation

- [ ] 2.1 Implement Dispatcher.Upstream in pkg/trigger/dispatcher.go with per-trigger error capture Check: go test ./pkg/trigger/... passes; the acceptance path is BLOCKED by the e2e step in the next section, which names the route and credential kinds
- [ ] 2.2 Send the upstream event from publishUnitState in the SQLite and PostgreSQL adapters, inside the fenced transaction Check: go test ./pkg/store/... passes
- [ ] 2.3 Publish the declared follower edge to the graph Check: go test ./internal/graph/... passes
- [ ] 2.4 Carry the trigger ID on the job and check each source read against the last saver Check: go test ./pkg/outputshare/... passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire routes and authorization actions on existing trigger routes Check: Policy check passes for the trigger routes with unit:manage-triggers; make check reports zero findings
- [ ] 4.2 Add e2e step against the real CLI Check: e2e with real OpenTofu over TLS on SQLite and PostgreSQL: POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with a personal access token holding unit:manage-triggers, run tofu apply on workspace A with a service access token, then GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} shows one plan job of workspace B; a repeat event starts nothing
- [ ] 4.3 Write the user docs page Check: page names which runs count as an apply; make docs-lint passes
- [ ] 4.4 Run the usability check Check: persona completes the path from the docs page alone; each error names one command
