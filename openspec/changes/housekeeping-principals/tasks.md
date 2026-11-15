SECURITY: this change is security code (ADR 250): it decides what the server does with no person in the loop. Each layer has its own hostile test, and each guard has a mutation test. Review: a cheap-model review of the change, a Claude review for security, and the Opus review of the stack.

Order: this change lands before run-triggers, add-schedule-triggers-due-claim, start-plan-on-push-review, write-state-version-unit-state-follows-trigger-start and job-service-unit-settings-pool, or in the same pull request as the first of them.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main, and run `rg -n 'history.ServerSubject|SweepSubject|cleanupSubject|IsNamespaceServiceSubject|NamespaceService\(' pkg internal --type go` to find each caller. Record each difference in design.md. Check: design.md lists the callers and the purposes.

## 1. Failing tests first

- [ ] 1.1 SECURITY. No hard-coded grant is left. Check: NEW test `TestNoHardCodedGrantLeft` in `pkg/access` fails on main.
- [ ] 1.2 SECURITY. Each sweep is attributed. Test file: NEW `pkg/job/housekeeping_attribution_test.go`. Check: NEW test `TestEachSweepIsAttributed` fails on main.
- [ ] 1.3 SECURITY. A revoke stops only its sweep. Test file: NEW `pkg/job/housekeeping_revoke_test.go`. Check: NEW test `TestRevokeStopsOnlyItsSweep` fails on main.
- [ ] 1.4 SECURITY. No credential and no out-of-purpose grant for a housekeeping principal. Test files: NEW `pkg/identity/housekeeping_auth_test.go` and NEW `pkg/access/housekeeping_grant_test.go`. Check: NEW tests `TestHousekeepingPrincipalCannotAuthenticate` and `TestGrantOutOfPurposeRefused` fail on main.
- [ ] 1.5 Conformance cases for the install principals and grants, on both dialects. Check: NEW cases in `pkg/principal/principaltest` fail on main.

## 2. Build

- [ ] 2.1 SECURITY. In the NEW file `pkg/principal/housekeeping.go`, add NEW `HousekeepingPurpose` and NEW `principal.Housekeeping`, the `builtin` source, and the install step that writes the principals and their stored grants (baseline edit, ADR 322). Check: 1.5 passes on SQLite and PostgreSQL.
- [ ] 2.2 SECURITY. Remove the namespace service principal: `pkg/identity/namespace_service.go`, `authorizeNamespaceService`, `NamespaceServiceRule`, the guard, and the checks in `pkg/trigger` and `pkg/job`. Check: `rg -n 'NamespaceService|namespace-service' pkg internal --type go` gives no line; 1.1 passes.
- [ ] 2.3 SECURITY. Each housekeeping service takes its principal and authorizes before it acts, starting in `pkg/job/artifactsweep.go:617`. Check: 1.2 and 1.3 pass; removing the authorize call of one service makes 1.3 fail (mutation list in the pull request).
- [ ] 2.4 SECURITY. Refuse credentials and out-of-purpose grants in `pkg/access/service.go:1610`. Check: 1.4 passes.
- [ ] 2.5 Update `GLOSSARY.md` (file on main; path `GLOSSARY.md`): remove "Namespace service principal", add "Housekeeping principal". Check: `make docs-lint` passes.
- [ ] 2.6 Add the line of each NEW error code (the code and its arguments, as `TestNewCodesHaveTheirLine` prints it) in its place in the name order: `principal.housekeeping` in `internal/errcode/testdata/catalog/principal.txt`; `access.grant-out-of-purpose` in `internal/errcode/testdata/catalog/access.txt`. No route: the test reads the catalog only. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 3. Definition of done

- [ ] 3.1 E2e: BLOCKED by the NEW routes `GET /api/dustpan/v1/principals` and `GET /api/dustpan/v1/principals/{principal}/grants`, which main lacks. Use an administrator access token, then the existing `/api/server/principals/{}/grants/{}` to revoke, and see the denial of its next run. Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Add the new file `docs/explanation/housekeeping-principals.md`. Check: `make docs-lint` passes, and a test runs each command of the page.
- [ ] 3.3 Usability check: the operator finds who ran each sweep from the docs page alone. Check: the check is recorded in the pull request. A person's step.
- [ ] 3.4 Full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
