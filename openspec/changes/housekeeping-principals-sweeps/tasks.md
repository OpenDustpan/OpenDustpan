SECURITY: this change is security code (ADR 250): it decides what the server does with no person in the loop. Each layer has its own hostile test, and each guard has a mutation test. Review: a cheap-model review of the change, a Claude review for security, and the Opus review of the stack.

Order: part 1 (`housekeeping-principals-install`) is on main first.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main, and run `rg -n 'history.ServerSubject|SweepSubject|cleanupSubject' pkg internal --type go` to find each caller. Record each difference in the pull request body. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 SECURITY. Each sweep is attributed. Test file: NEW `pkg/job/housekeeping_attribution_test.go`. Check: NEW test `TestEachSweepIsAttributed` fails on main.
- [ ] 1.2 SECURITY. A revoke stops only its sweep. Test file: NEW `pkg/job/housekeeping_revoke_test.go`. Check: NEW test `TestRevokeStopsOnlyItsSweep` fails on main.

## 2. Build

- [ ] 2.1 SECURITY. Each housekeeping service takes its principal and authorizes before it acts, for each purpose of the design document ("Callers on main"): `pkg/job/artifactsweep.go:654`, `pkg/job/reconcile.go`, `pkg/savedview/service.go`, `pkg/history/redaction.go` (and `pkg/history/history.go`, with the test helper `internal/history/historytest/redactor.go`), `internal/localusersvc/locks.go`, `internal/agent/reaper.go`, `pkg/stack/superseded_sweep.go` and `pkg/workspace/service.go`. Check: 1.1 and 1.2 pass; removing the authorize call of one service makes 1.2 fail (mutation list in the pull request).
- [ ] 2.2 Pass each principal to its service at the construction in `pkg/dustpan/dustpan.go`. Do not edit the namespace service principal, its authorizer rule, its allowlist rule, its guard or the checks in the trigger and job start path: the dispatcher starts every triggered plan with it. No route: the test builds the module and scans the housekeeping services only. Check: `go build ./...` passes and no housekeeping service uses `identity.NamespaceService`.

## 3. Definition of done

- [ ] 3.2 Full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
