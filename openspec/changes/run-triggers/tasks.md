## 1. failing tests first

- [ ] 1.1 Add failing domain tests for plan-only start and trigger shape checks. Check: go test ./pkg/trigger/... -run TestTriggerPlanOnly -count=1
- [ ] 1.2 Add failing route tests for auth actions and audit entries. Check: go test ./internal/api/triggers/... -run TestTriggerRoutes -count=1
- [ ] 1.3 Add failing e2e step for a trigger-started plan against the real CLI. Check: go test ./e2e -run TestTriggers -count=1

## 1b. Run-as principal (SECURITY, ADR 369 decision 7, ADR 370)

- [ ] 1b.1 SECURITY. Failing hostile tests: a person as run-as principal, a service principal with no act-as edge, an inline principal with a grant above the author, a suspended run-as principal, a read of a triggered job with the grants of the saver. Check: NEW test `TestTriggerRunAsHostile` in `pkg/trigger` fails on main.
- [ ] 1b.2 SECURITY. The same table at the HTTP handler. Check: NEW test `TestTriggerRoutesRunAsHostile` fails on main.
- [ ] 1b.3 SECURITY. Store the stable ID of the run-as principal on the trigger, with the hard check in the constructor of the trigger and the act-as check in the service. Check: both tests pass; removing each check makes a test fail (mutation list in the pull request).
- [ ] 1b.4 SECURITY. Delete `authorizeNamespaceService` and each use of the namespace service principal in the start path (ADR 370). Check: `rg -n 'IsNamespaceServiceSubject|NamespaceService\(' pkg internal` gives no line in the trigger and job start path.

## 2. build

- [ ] 2.1 Implement trigger service with plan-only starter and fenced store. Check: go test ./pkg/trigger/... -count=1
- [ ] 2.2 Wire routes with declared actions, errors and audit entries. Check: go test ./internal/api/triggers/... -count=1
- [ ] 2.3 Drive e2e step with the real CLI and the real directory and the started plan visible. Check: make e2e E2E_RUN=triggers
- [ ] 2.4 Extend the run triggers guide in the same pull request. Check: ls docs/guides/run-triggers.md

## 3. definition of done

- [ ] 3.1 Confirm wiring: trigger routes reachable from the server with auth actions. Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers returns 200 with trigger rows
- [ ] 3.2 Confirm e2e step against the real CLI and the real directory. Check: make e2e E2E_RUN=triggers passes on SQLite and PostgreSQL
- [ ] 3.3 Confirm docs page and usability check from the guide alone. Check: docs page command runs as written
- [ ] 3.4 Confirm make check with zero findings. Check: make check reports zero findings
