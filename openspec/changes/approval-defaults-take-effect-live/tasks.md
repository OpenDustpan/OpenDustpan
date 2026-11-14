## 1. Failing tests

- [ ] 1.1 Add failing service test for Pulumi apply that waits under an approval default. Check: TestJobApprovalWaitPulumiFailsOnMain shows missing wait for a Pulumi apply under a default.
- [ ] 1.2 Add failing service test for OpenTofu apply that waits under an approval default. Check: TestJobApprovalWaitOpenTofuFailsOnMain shows missing wait for an OpenTofu run under a default.
- [ ] 1.3 Add failing service test for apply with no default that needs no approval. Check: TestJobNoDefaultNeedsNoApprovalFailsOnMain shows an apply without default proceeds.
- [ ] 1.4 Add failing test for recheck at change.apply with a new requirement. Check: TestJobRecheckApplyFailsOnMain shows a default saved after start blocks the apply dispatch.
- [ ] 1.5 Add failing test for recheck-only-adds on removal during wait. Check: TestJobRecheckOnlyAddsFailsOnMain shows removal of a default while in wait does not release the wait.
- [ ] 1.6 Add failing test for deny gate and engine error fail-closed. Check: TestJobDenyAndErrorFailClosedFailsOnMain shows deny refusal and enforce-mode error stop.
- [ ] 1.7 Add failing test for unavailable facts fail-closed. Check: TestJobUnavailableFactsFailsOnMain shows unavailable facts keep the wait instead of skipping it.
- [ ] 1.8 Add failing test for evaluate mode with no block. Check: TestJobEvaluateModeFailsOnMain shows an evaluate-mode default records without wait.
- [ ] 1.9 Add failing test for wait persistence across restart. Check: TestJobWaitSurvivesRestartFailsOnMain shows stored wait and signatures survive restart.
- [ ] 1.10 Add failing test for authorization refusal at apply start. Check: TestJobPlanRightsRefusedFailsOnMain shows 403 for apply with plan-only rights.

## 2. Wiring

- [ ] 2.1 Wire jobgate.Decider into the job service points from #968 with gate evaluation from #899. Check: go test ./pkg/gate/jobgate/... ./internal/jobsvc/... passes with the Decider wired at change.propose, change.planned and change.apply.
- [ ] 2.2 Record each evaluation with gate versions and resume on the latest requirement set. Check: go test -race ./... for the touched packages passes and decision records name the gate, the gate versions and the schema version.

## 3. Definition of done

- [ ] 3.1 Add e2e step for a Pulumi update and an OpenTofu run that wait and continue, with deny path. Check: e2e step drives the real Pulumi CLI and the real OpenTofu CLI through TLS and shows approval wait then continue after approvals, plus deny refusal with the record named.
- [ ] 3.2 Write the docs page for approval defaults on live jobs. Check: Docs page runs as written and shows the default, the wait state and the approve command.
- [ ] 3.3 Run usability check from the docs page alone and make check with zero findings. Check: make check reports zero findings.
