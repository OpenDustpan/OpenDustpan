## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add Upstream idempotency and ErrVersionMismatch conformance cases that map to the One run per version and Older version scenarios Check: pkg/trigger/triggertest Upstream cases for applied, duplicate intake, older version, and concurrent older arrival fail on main
- [ ] 1.2 Extend save conformance cases that map to the Cycle refused at save and Follower count scenarios Check: pkg/trigger/triggertest save cases for cycle path and 51st follower fail on main
- [ ] 1.3 Add saver read-check conformance cases that map to the Saver read check scenarios Check: pkg/outputshare conformance cases with missing, deleted, disabled, and grant-lost saver deny and fail on main

## 2. Implementation

- [ ] 2.1 Implement Dispatcher.Upstream in pkg/trigger/dispatcher.go over the final-version store with per-trigger error capture on the trigger row Check: go test ./pkg/trigger/... passes; the acceptance path is POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire called with a personal access token that holds unit:manage-triggers
- [ ] 2.2 Wire the OpenTofu final-version writer in pkg/workspace/state.go as the event sender Check: go test ./pkg/workspace/... passes; the acceptance path is ANY /api/v2/state-versions called with a service access token; Pulumi emits nothing in this slice
- [ ] 2.3 Publish the declared follower edge to the graph Check: go test ./internal/graph/... passes; follows trigger publishes a declared edge and safe delete names followers
- [ ] 2.4 Carry the trigger ID on the job and check each source output read against the last saver Check: go test ./pkg/outputshare/... passes; missing saver denies with an audit entry

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire routes and authorization actions on existing trigger routes Check: Policy check passes for ANY/GET/POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with unit:manage-triggers; make check reports zero findings
- [ ] 4.2 Add e2e step against the real CLI Check: e2e with the real OpenTofu CLI over TLS on SQLite and PostgreSQL applies workspace A and the plan job of workspace B starts once; a repeat event starts nothing
- [ ] 4.3 Write the user docs page in the same pull request Check: docs page names which runs count as an apply for OpenTofu and the Pulumi rule for later; make docs-lint passes
- [ ] 4.4 Run the usability check Check: persona completes the path from the docs page alone; each error names the one command that helps
