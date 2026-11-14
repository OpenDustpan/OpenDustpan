## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add Upstream conformance cases for applied, duplicate, older and concurrent older version Check: pkg/trigger/triggertest Upstream cases fail on main
- [ ] 1.2 Add saver read-check cases for missing, deleted, disabled and grant-lost saver Check: pkg/outputshare/outputsharetest cases fail on main

## 2. Implementation

- [ ] 2.1 Implement Dispatcher.Upstream with per-trigger error capture in pkg/trigger/dispatcher.go:110 (func (d *dispatcher) Upstream). Check: go test ./pkg/trigger/... passes; the acceptance path is the route POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire called with a personal access token
- [ ] 2.2 Send the upstream event when OpenTofu writes a final state version in pkg/dustpan/triggers_versions.go (NEW). Check: go test ./pkg/store/... ./pkg/dustpan/... passes
- [ ] 2.3 Publish the declared follower edge to the graph in pkg/trigger/edges.go (NEW). Check: go test ./internal/graph/... passes
- [ ] 2.4 Carry the trigger ID on the job and check source output reads against the last saver in pkg/outputshare/saver_check.go (NEW). Check: go test ./pkg/outputshare/... passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Check authorization actions on the trigger routes Check: route test passes; make check reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Add e2e step against the real CLI in the NEW file e2e/follows_trigger_test.go, with route POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers and a personal access token. Check: e2e with real OpenTofu: POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with a personal access token, run tofu apply on workspace A, then GET the trigger shows one plan job of workspace B; a repeat event starts nothing. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Write the user docs page in the NEW file docs/guides/follows-triggers.md. Check: make docs-lint passes
- [ ] 4.4 Run the usability check Check: persona completes the path from the docs page alone; each error names one command. A person's step.
