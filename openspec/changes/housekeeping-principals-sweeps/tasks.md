SECURITY: this change is security code (ADR 250): it decides what the server does with no person in the loop. Each layer has its own hostile test, and each guard has a mutation test. Review: a cheap-model review of the change, a Claude review for security, and the Opus review of the stack.

Order: part 1 (`housekeeping-principals-install`) is on main first. It makes the principals, the roles, the actions (including `job:reconcile`) and the grants on each namespace.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main, and run `rg -n 'history.ServerSubject|SweepSubject|cleanupSubject' pkg internal --type go` to find each caller. Record each difference in the pull request body. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 SECURITY. Each sweep is attributed. Test file: NEW `pkg/job/housekeeping_attribution_test.go`. Check: NEW test `TestEachSweepIsAttributed` fails on main.
- [ ] 1.2 SECURITY. A revoke stops only its sweep. Test file: NEW `pkg/job/housekeeping_revoke_test.go`. Check: NEW test `TestRevokeStopsOnlyItsSweep` fails on main.

- [ ] 1.3 SECURITY. The reconcile holds no start action, and the namespace purposes work through their stored grants on the namespace. The test runs in the current mode (the setting `access.installationGrantsReachNamespaces` does not exist on main); the run with the setting `false` is task 1.4. Test files: NEW `pkg/job/housekeeping_reconcile_test.go` (`TestReconcileHoldsNoStartAction`) and NEW `pkg/job/housekeeping_reach_test.go` (`TestNamespacePurposesWorkWithoutInstallationReach`). Check: both fail on main.
- [ ] 1.4 The namespace purposes run with the setting `access.installationGrantsReachNamespaces` set to `false` (the setting is a NEW setting of the change `installation-grants-reach-namespaces-setting`; the test sets it through the settings port, no seam in product code). In NEW `pkg/job/reach_namespaces_setting_test.go` run `TestReachNamespacesSettingNamespacePurposes`: the retention of history, the issuer rotation, the reconcile of jobs and the clean-up of saved views run on a namespace with the setting `false`. The test builds its own scenario and calls no test function of other changes. Write it with a table that has a row for the value `true` and a row for the value `false`. Check: the test passes with the row `false` after 2.1 and fails before it, because the service principals of the housekeeping hold their actions on the namespace only through stored grants on the namespace (2.1), and `pkg/job/reconcile.go` has no authorizer on main.

## 2. Build

- [ ] 2.1 SECURITY. Each housekeeping service takes its principal and authorizes before it acts, for each purpose of the design document ("Callers on main"): `pkg/job/artifactsweep.go:654`, `pkg/job/reconcile.go`, `pkg/savedview/service.go`, `pkg/history/redaction.go` (and `pkg/history/history.go`, with the test helper `internal/history/historytest/redactor.go`), `internal/localusersvc/locks.go`, `internal/agent/reaper.go`, `pkg/stack/superseded_sweep.go` and `pkg/workspace/service.go`. Each service checks the action and the object of decision 5 of the design document. `pkg/job/reconcile.go` checks `job:reconcile` (NEW in part 1) inside `reconcileRun.apply`, and `reconcileAction` stays only for the audit entry. `internal/agent/reaper.go` makes two checks. Check: 1.1, 1.2 and 1.3 pass; removing the authorize call of one service makes 1.2 fail (mutation list in the pull request).
- [ ] 2.2 Pass each principal to its service at the construction in `pkg/dustpan/dustpan.go`. Do not edit the namespace service principal, its authorizer rule, its allowlist rule, its guard or the checks in the trigger and job start path: the dispatcher starts every triggered plan with it. No route: the test builds the module and scans the housekeeping services only. Check: `go build ./...` passes and no housekeeping service uses `identity.NamespaceService`.

## 3. Definition of done

- [ ] 3.1 Agent pre-run (human run-through in person-steps (housekeeping-principals-sweeps 3.1)): Usability check: an operator finds who ran each of these sweeps in the audit log from the entry alone. Check: the check is recorded in the pull request. Record the transcript in the pull request body.
- [ ] 3.2 Full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
