## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Add failing domain tests for plan-only start, cycle path and limits in pkg/trigger/plan_only_test.go (NEW). Check: go test ./pkg/trigger/... -run TestTriggerPlanOnly -count=1
- [ ] 1.2 Add failing route tests for declared actions, audit entries and parallel saves by extending the existing file internal/api/triggers/routes_test.go. Check: go test ./internal/api/triggers/... -run TestTriggerRoutes -count=1
- [ ] 1.3 Add a failing e2e step in e2e/triggers_test.go that creates a trigger with POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers and fires it with POST .../triggers/{trigger_id}/fire, both with an access token of a person, then reads the plan job. BLOCKED by the runners change for the job run. Check: go test -tags e2e ./e2e -run TestTriggers -count=1. Confirmed by: CI (the agent writes the e2e test).

## 2. build

- [ ] 1.4 SECURITY. Add failing test `TestNoHardCodedGrantLeft` in the existing package `pkg/access` (new file `pkg/access/no_hard_coded_grant_test.go`): the authorizer has no rule in code that grants an action to a subject. Check: `TestNoHardCodedGrantLeft` (NEW symbol, not on main) fails on main because `pkg/access/authorizer.go:119` decides a subject in code.
- [ ] 2.0 SECURITY. Remove the namespace service principal (ADR 370 decision 1) together with the run-as principal that replaces it: `pkg/identity/namespace_service.go`, `authorizeNamespaceService`, `NamespaceServiceRule`, the guard, the checks in `pkg/trigger` and `pkg/job`, and the glossary term. Check: `rg -n 'NamespaceService|namespace-service' pkg internal --type go` gives no line; `TestNoHardCodedGrantLeft` passes.
- [ ] 2.1 Implement plan-only starter, cycle check and limits in pkg/trigger/service.go (NEW). Check: go test ./pkg/trigger/... -count=1
- [ ] 2.2 Wire routes with declared actions and audit entries. Edit the route declaration file internal/api/triggers/routes.go and the adapter file internal/api/triggers/serve.go. Check: go test ./internal/api/triggers/... -count=1
- [ ] 2.3 Extend the run triggers guide. Its commands use GET and POST on /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with an access token of a person. The task adds the NEW top-level test `TestTriggersGuide` in a `_test.go` file of `e2e`, so that the Check runs it. Check: go test -tags e2e ./e2e -run TestTriggersGuide -count=1 runs each command of docs/guides/run-triggers.md as written. `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Confirm the route wiring. Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with an access token of a person returns 200
- [ ] 4.2 Confirm the e2e step: POST .../triggers/{trigger_id}/fire with an access token of a person starts a plan job on the real server. BLOCKED by the runners change for the job run. Check: make e2e E2E_RUN=triggers passes on SQLite and PostgreSQL. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.4 Confirm make check. Check: make check reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
