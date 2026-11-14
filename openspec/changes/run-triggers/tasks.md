## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Add failing domain tests for plan-only start, cycle path, limits and same-namespace rule. Check: go test ./pkg/trigger/... -run TestTriggerPlanOnly -count=1
- [ ] 1.2 Add failing route tests for declared actions, audit entries and parallel saves. Check: go test ./internal/api/triggers/... -run TestTriggerRoutes -count=1
- [ ] 1.3 Add a failing e2e step: POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire with an access token of a person, then read the plan job. Check: go test ./e2e -run TestTriggers -count=1

## 2. build

- [ ] 2.1 Implement the trigger service with plan-only starter, cycle check and limits. Check: go test ./pkg/trigger/... -count=1
- [ ] 2.2 Wire routes with declared actions, errors and audit entries. Check: go test ./internal/api/triggers/... -count=1
- [ ] 2.3 Extend the run triggers guide. Check: ls docs/guides/run-triggers.md

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Confirm wiring of the trigger routes. Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with an access token returns 200
- [ ] 4.2 Confirm the e2e step: fire route with an access token of a person against the real server and real CLI. Check: make e2e E2E_RUN=triggers passes on SQLite and PostgreSQL
- [ ] 4.3 Confirm the docs page usability check. Check: docs page command runs as written
- [ ] 4.4 Confirm make check. Check: make check reports zero findings
