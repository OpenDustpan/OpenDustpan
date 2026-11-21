## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" and each cite of the design at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add `pkg/job/machine_approve_test.go` (black-box, `t.Parallel()`, table-driven) with the rows 1 to 3, 5, 6, 8 to 15, 18 and 20 to 22 of the design table. Check: `go test ./pkg/job -run TestApproveMachine` fails on main, because `checkApprover` reads no kind or credential (`pkg/job/service_approve.go:208`).
- [ ] 1.2 Add to `pkg/job/match_test.go` the rows 6, 12 and 16: a machine approval fills a slot only on a gate that holds the subject in `MachineIDs` and has no stored window, and never for the starter. Check: `go test ./pkg/job -run TestGatesStatusMachine` fails on main, because `Approval.Machine` (NEW) does not exist.
- [ ] 1.3 Add a table test of the rule save for row 4 and the other bad entries of `machineIds`. Check: the test fails on main, because `Approvers` has no `MachineIDs` (`pkg/gate/rules.go:147`).
- [ ] 1.4 Add a conformance case to `pkg/job/jobtest/approve.go`: a job with a gate that holds `MachineIDs` and an approval with `Machine` and `Credential` round-trips on both dialects. Check: the job store conformance suite fails on main, because `approvalBody` has no `machine` field.
- [ ] 1.5 Add to `internal/servertest/jobs_api_test.go` the test `TestMachineApprovalJobsAPI`: a named service principal approves with its access token and the job runs; an unnamed one gets 403; a body with `machine: false` is ignored. Check: the test fails on main.

## 2. Implementation

- [ ] 2.1 `MachineIDs` on `Approvers` (`pkg/gate/rules.go:147`), the save check in the config service, `Gate.MachineIDs`, and the copy in `gatesOf` (`pkg/gate/jobgate/jobgate.go`), in the same codec change as `UserIDs` of task 5.1 of the other change. The shape of the field waits for open question 5. Check: task 1.3 passes.
- [ ] 2.2 The kind rule in `checkApprover` (`pkg/job/service_approve.go:208`): the function of design decision 1, the `machineIds` check by gate, and the refusal of a job token, a trust token and an agent. Check: the rows of task 1.1 for kinds pass.
- [ ] 2.3 The self-approval rule for a machine as its own check, not inside the `AllowSelf` check (`pkg/job/service_approve.go:225`), in `GatesStatus` too. The check of `Token.CreatedBy` waits for open question 1, and the check of the author of a plan waits for the record of the upload change. Check: the row 6 of tasks 1.1 and 1.2 passes.
- [ ] 2.4 The window rule: `For` of a machine approval holds only the gates with no stored window, in `checkApproval` (`pkg/job/service_approve.go:83`) after task 5.4 of the other change. The reading of the stored window waits for open question 2, and the `destroy` rows wait for open question 6. Check: the rows 8 to 12 pass.
- [ ] 2.5 The marks: `Approval.Machine` and `Approval.Credential`, `approvalEntry` (`pkg/job/service_approve.go:391`) with `machine` and `credential`, `Signature.Machine` and `Signature.TokenID` (`pkg/gate/record.go:91`), the codec in `pkg/store/internal/jobstore/body.go`, and the audit entry of a refusal. The SQL baselines do not change. Check: tasks 1.4 and 1.5 pass, and `git diff --stat -- pkg/store/sqlite/migrations pkg/store/postgres/migrations` is empty.
- [ ] 2.6 The API: `approvers.machineIds` on the approval rule and approval default routes, `gates[].machineIds` and `approvals[].machine` on the job response, in `internal/api/jobs/serve.go`. Run `make generate` and `make clients-generate`. Check: task 1.5 passes, and the diff of generated files comes from the generators only.
- [ ] 2.7 Set the round-trip budgets of the changed request flows in the existing files `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go` and `internal/servertest/roundtrips_test.go`. Check: the budget test passes.

## 3. Security tests

- [ ] 3.1 For each guard of the layer table in the design, add the test that fails when the guard is removed, and run each one against a copy of the code with the guard removed. Check: the pull request body lists each guard, its test and the failure seen with the guard removed.
- [ ] 3.2 Each redundant check carries the comment "Defense in depth: ..." that names the layers it does not rely on, with no ADR number. Check: `rg -n 'Defense in depth' pkg/job/service_approve.go pkg/job/match.go` lists each one.

## 4. Definition of done

- [ ] 4.1 Add the e2e step in `e2e/approval_rules_test.go` (or a new file when it is too large): a rule that names a service principal, a Pulumi CLI update that waits, and the approval by the service principal with its access token through `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals`. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 In `docs/guides/approval-rules.md`: one section on `machineIds` (who may approve with a token, the four conditions, and the one command to list the entries). Each command runs in the e2e step of task 4.1. Check: `make docs-lint` passes.
- [ ] 4.5 Run `make check`. Check: zero findings. Confirmed by: CI (the agent runs the fast checks only).

## 5. Records
