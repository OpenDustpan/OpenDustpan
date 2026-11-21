## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each row of "What the change was and what main already does" at its file and line on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add a failing test for the start of an apply and a destroy of an OpenTofu workspace in `pkg/dustpan/jobservice_internal_test.go`. It replaces `TestJobPlannerRefusesApplyAndDestroyWithTheOldAnswer` for OpenTofu and keeps the refusal for another tool. Check: `TestJobPlannerStartsApplyAndDestroyOfOpenTofuFailsOnMain` shows the 422 on main.
- [ ] 1.2 Add a failing service test in `pkg/job/service_test.go` for an apply job under a default of two approvers: it waits after the plan phase and is queued after two approvers. Check: `TestApplyJobWaitsForTheDefaultFailsOnMain` fails on main.
- [ ] 1.3 Add failing reconcile tests in `pkg/job/reconcile_test.go` and a store suite case in `pkg/job/jobtest/reconcile.go` for the recheck: a default added after the start gates the dispatch, a removed default releases nothing, and an unreadable default blocks the dispatch. Check: `TestReconcileRechecksTheDefaultsFailsOnMain` fails on main.
- [ ] 1.4 Add a failing test in `pkg/job/service_start_internal_test.go` for the policy version in the audit entry of the start. Check: `TestStartEntryNamesThePolicyVersionFailsOnMain` fails on main.
- [ ] 1.5 SECURITY. Add one table of hostile cases for the apply start at the HTTP handler in `internal/api/jobs/start_test.go`: an operation with control characters, an apply by a principal with only the plan action, and an apply on a unit of another tool. Check: each case answers 400, 403 or 422 and stores no job, and the test fails when its guard is removed.
- [ ] 1.6 SECURITY. Add hostile tests for the recheck in `pkg/job/reconcile_test.go`: a reader that returns defaults of another namespace, and a reader that returns an error. Check: the job is not dispatched in both cases.

## 2. Wiring

- [ ] 2.1 Lift the refusal for an apply and a destroy of an OpenTofu workspace in `pkg/dustpan/jobservice.go` (`checkPlanRequest`, `startsOnRunner`). Keep the refusal for another tool, for edits and for another executor. Check: `go test ./pkg/dustpan/...` passes.
- [ ] 2.2 Add the recheck to the reconcile in `pkg/job/reconcile.go`: a port that reads the decision for a queued job, the change that adds a gate to the stored phase, and the reason that names the default. The store port and its conformance suite get the change before any adapter implements it. Check: `go test ./pkg/job/... ./pkg/store/...` passes, with `-timeout 60m` for the store packages.
- [ ] 2.3 Write the policy version of the decision in the audit entry of the start in `pkg/job/service_start.go`. Check: `go test ./pkg/job/...` passes.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Add an e2e step in the NEW file `e2e/jobs_apply_approval_test.go` that starts an apply of an OpenTofu workspace on a runner with the real OpenTofu CLI, sets the default with `PUT /api/dustpan/v1/namespaces/{namespace}/approval-default`, shows `awaiting-approval`, approves with two principals and shows the apply. Check: the step passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Update `docs/guides/approval-rules.md` and `docs/reference/jobs-api.md` for the apply start, the wait and the recheck, and remove the lines that say that an apply is not accepted. Check: the docs commands run in an e2e step in the NEW file `e2e/jobs_apply_approval_docs_test.go`, and `make docs-lint` passes. Confirmed by: CI.
