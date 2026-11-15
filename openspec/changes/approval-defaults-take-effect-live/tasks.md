## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing service test in NEW file internal/jobsvc/approval_wait_test.go for Pulumi apply that waits under an approval default. Check: TestJobApprovalWaitPulumiFailsOnMain shows missing wait for a Pulumi apply under a default.
- [ ] 1.2 Add failing service test in NEW file internal/jobsvc/approval_wait_test.go for OpenTofu apply that waits under an approval default. Check: TestJobApprovalWaitOpenTofuFailsOnMain shows missing wait for an OpenTofu run under a default.
- [ ] 1.3 Add failing service test in NEW file internal/jobsvc/approval_wait_test.go for apply with no default that needs no approval. Check: TestJobNoDefaultNeedsNoApprovalFailsOnMain shows an apply without default proceeds.
- [ ] 1.4 Add failing test in NEW file internal/jobsvc/approval_recheck_test.go for recheck at change.apply with a new requirement. Check: TestJobRecheckApplyFailsOnMain shows a default saved after start blocks the apply dispatch.
- [ ] 1.5 Add failing test in NEW file internal/jobsvc/approval_recheck_test.go for recheck-only-adds on removal during wait. Check: TestJobRecheckOnlyAddsFailsOnMain shows removal of a default while in wait does not release the wait.
- [ ] 1.6 Add failing test in NEW file internal/jobsvc/approval_combine_test.go for deny plus approval requirement combine order. Check: TestJobDenyBeatsApprovalFailsOnMain shows deny stops the change when an approval requirement also matches.
- [ ] 1.7 Add failing test in NEW file internal/jobsvc/approval_combine_test.go for deny gate and engine error fail-closed. Check: TestJobDenyAndErrorFailClosedFailsOnMain shows deny refusal and enforce-mode error stop.
- [ ] 1.8 Add failing test in NEW file internal/jobsvc/approval_facts_test.go for unavailable facts fail-closed. Check: TestJobUnavailableFactsFailsOnMain shows unavailable facts keep the wait with error result.
- [ ] 1.9 Add failing test in NEW file internal/jobsvc/approval_facts_test.go for evaluate mode with no block. Check: TestJobEvaluateModeFailsOnMain shows an evaluate-mode default records without wait.
- [ ] 1.10 Add failing test in NEW file internal/jobsvc/approval_persist_test.go for wait persistence across restart and lease expiry. Check: TestJobWaitSurvivesRestartFailsOnMain shows stored wait and signatures survive restart and lease loss.
- [ ] 1.11 Add failing test in NEW file internal/jobsvc/approval_authz_test.go for authorization refusal at StartJob (NEW). Check: TestJobPlanRightsRefusedFailsOnMain shows 403 for apply with plan-only rights.
- [ ] 1.12 Add failing test in NEW file internal/jobsvc/approval_record_test.go for decision record contents. Check: TestJobDecisionRecordNamesGateFailsOnMain shows the record holds gate versions and schema version.

- [ ] 1.13 SECURITY. Add hostile test TestJobBoundaryRejectsHostileInput in internal/jobsvc/approval_authz_test.go. It sends an empty subject and a malformed unit reference to StartJob. Check: TestJobBoundaryRejectsHostileInput shows refusal and zero engine calls.
- [ ] 1.14 SECURITY. Add hostile test TestStateWriteReplayPriorRevisionRefused in NEW file internal/jobsvc/state_write_guard_test.go. It replays a prior-revision approval at the tool write handler. Check: TestStateWriteReplayPriorRevisionRefused shows refusal with the record named and no state write.

## 2. Wiring

- [ ] 2.1 Add the new file internal/jobsvc and wire jobgate.Decider into the job service points from #968 with gate evaluation from #899. Check: go test ./pkg/gate/jobgate/... passes with the Decider wired at change.propose, change.planned and change.apply.
- [ ] 2.2 Record each evaluation with gate versions and resume on the latest requirement set inside one store transaction, in NEW file internal/jobsvc/approval_record.go. Check: go test -race ./... for the touched packages passes and decision records name the gate, the gate versions and the schema version.

- [ ] 2.3 SECURITY. Build the record version check in the tool write handlers in NEW file internal/jobsvc/state_write_guard.go. The handler refuses a state write when the stored record version does not match the dispatched revision. Check: go test ./internal/jobsvc/... passes and a state write with a stale record version is refused with the record named.
- [ ] 2.4 SECURITY. Build the boundary validation of the job service port adapter in NEW file internal/jobsvc/boundary.go. It refuses an empty subject and a malformed unit reference before any Decider call. Check: go test ./internal/jobsvc/... passes and no engine call happens for hostile input.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Add e2e step in NEW file e2e/approval_defaults_take_effect_live_test.go for a Pulumi update and an OpenTofu run that wait and continue, with deny path. Check: e2e step on PUT /api/dustpan/v1/namespaces/{namespace}/approval-default with an access-token credential drives the real Pulumi CLI and the real OpenTofu CLI through TLS and shows approval wait then continue after approvals, plus deny refusal with the record named. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the docs page for approval defaults on live jobs in NEW file docs/guides/approval-defaults-live.md. Check: Docs page runs as written and shows the default, the wait state and the approve command. Confirmed by: CI (an e2e step in the NEW file e2e/approval_defaults_take_effect_live_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.3 Run usability check from the docs page alone and make check with zero findings. Check: make check reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
