SECURITY: this change is security code (ADR 250): it decides what the server does with no person in the loop. Each layer has its own hostile test, and each guard has a mutation test. Review: a cheap-model review of the change, a Claude review for security, and the Opus review of the stack.

Order: this part (part 3) starts after `housekeeping-principals-install` and `housekeeping-principals-sweeps` are on main, and after the pull request of the change `mtls-mtls-exchange-token` is in the merge queue, because both edit `pkg/accesstoken`. This change lands before run-triggers, add-schedule-triggers-due-claim, start-plan-on-push-review, write-state-version-unit-state-follows-trigger-start and job-service-unit-settings-pool, or in the same pull request as the first of them.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main, and run `rg -n 'IsNamespaceServiceSubject|NamespaceService\(' pkg internal --type go` to find each caller. Record each difference in the pull request body. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 SECURITY. No credential and no out-of-purpose grant for a housekeeping principal. Test files: NEW `pkg/identity/housekeeping_auth_test.go` and NEW `pkg/access/housekeeping_grant_test.go`. Check: NEW tests `TestHousekeepingPrincipalCannotAuthenticate` and `TestGrantOutOfPurposeRefused` fail on main.
- [ ] 1.2 SECURITY. Extend `TestEachSweepIsAttributed` and `TestRevokeStopsOnlyItsSweep` (the NEW files `pkg/job/housekeeping_attribution_test.go` and `pkg/job/housekeeping_revoke_test.go` of part 2) with the sweep of access tokens, the expiry of temporary administrators and the rotation of issuer keys. Check: the new cases fail on main.

## 2. Build

- [ ] 2.1 SECURITY. Each service checks the action of its row in the roles table of part 1. The sweep of access tokens, the expiry of temporary administrators and the rotation of issuer keys take their principal and authorize before they act: `pkg/accesstoken/policy.go`, `pkg/tempadmin/service.go`, `pkg/tempadmin/authenticator.go`, `internal/issuersvc/settings.go` and `internal/issuersvc/rotate.go`, with the principals passed at the construction in `pkg/dustpan/dustpan.go`. Check: 1.2 passes; removing the authorize call of one service makes 1.2 fail (mutation list in the pull request).
- [ ] 2.2 SECURITY. Refuse credentials and out-of-purpose grants in `pkg/access/service.go:1630`, and refuse a credential, a session and a token for a housekeeping subject in each layer of decision 2 of the design document: `pkg/principal/namespace_service_guard.go`, `pkg/principal/principal.go:352`, `internal/sessionauth/sessionauth.go:219`, `pkg/accesstoken/service.go:680` and `pkg/accesstoken/service.go:1011`. Check: 1.1 passes, and a test of each layer fails when its guard is removed.
- [ ] 2.3 Keep the namespace service principal until the first trigger change removes it with the run-as principal that replaces it (ADR 370: "or with them"). Do not edit `pkg/identity/namespace_service.go`, `authorizeNamespaceService`, `NamespaceServiceRule` or the checks in `pkg/trigger` and `pkg/job`: the dispatcher starts every triggered plan with it (`pkg/trigger/dispatcher.go:249`, `pkg/trigger/manager.go:459`, `pkg/trigger/trigger.go:264`, `pkg/job/trigger.go:47`, `pkg/dustpan/triggers.go:262`). No route: the test builds the module and scans the housekeeping services only. Check: `go build ./...` passes and no housekeeping service uses `identity.NamespaceService`.
- [ ] 2.4 Add the line of each NEW error code (the code and its arguments, as `TestNewCodesHaveTheirLine` prints it) in its place in the name order: `principal.housekeeping` in `internal/errcode/testdata/catalog/principal.txt`; `access.grant-out-of-purpose` in `internal/errcode/testdata/catalog/access.txt`. No route: the test reads the catalog only. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 3. Definition of done

- [ ] 3.1 E2e: use an administrator access token. List the principals with the existing `GET /api/server/principals?kind=service&source=builtin`, read the grants with the existing `GET /api/server/principals/{principal}/grants`, revoke with the existing `/api/server/principals/{principal}/grants/{id}` route, and see the denial of the next run, in `e2e/principals_test.go`. Check: `make e2e` passes.
- [ ] 3.2 Add the new file `docs/explanation/housekeeping-principals.md`: each housekeeping principal, its purpose, its role and its schedule, and how to revoke and grant again. Check: `make docs-lint` passes, and a test runs each command of the page.
- [ ] 3.3 Agent pre-run (human run-through in person-steps (housekeeping-principals 3.3)): Usability check: the operator finds who ran each sweep from the docs page alone. Check: the check is recorded in the pull request. Record the transcript in the pull request body.
- [ ] 3.4 Full check. Check: `make check` reports zero findings.
