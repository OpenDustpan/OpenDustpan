## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing gate subject test through principalSubjects.Subject in pkg/gate/service_test.go. Check: TestGateJobTokenSubjectFromProductionSource in pkg/gate/service_test.go fails on main, passes after fix.
- [ ] 1.2 Add failing TFE unlocked declare test with deny gate in internal/api/tfe/state_write_test.go. Check: TestTfeUnlockedDeclareMeetsStateWrite in internal/api/tfe/state_write_test.go fails on main, passes after fix.
- [ ] 1.3 Add failing PassedApply hostile tests before pass and after fail in pkg/gate/service_test.go. Check: TestJobTokenWriteNeedsPassedApply in pkg/gate/service_test.go fails on main, passes after fix.

## 2. Fix

- [ ] 2.1 Fill Subject.Job with job ID, phase, units, and PassedApply and fail closed in pkg/dustpan/stateguard.go:335. Check: pkg/dustpan/stateguard.go builds and go test ./pkg/dustpan ./pkg/gate passes.
- [ ] 2.2 Evaluate PassedApply in jobTokenWrite with coded audited refusal in pkg/gate/service.go:261. Check: go test ./pkg/gate passes including PassedApply allow and refuse paths.
- [ ] 2.3 Record the NEW phase flag PassedApply when a phase passes change.apply in pkg/gate/service.go:261. Check: Phase record persists across restart and go test ./pkg/job passes.
- [ ] 2.4 Call state guard with unlocked-write and unit:write-unlocked in createStateVersion on unlocked override in internal/api/tfe/state_write.go:168. Check: go test ./internal/api/tfe passes and refusal is JSON:API 403.

## 3. Definition of done

- [ ] 3.1 Add e2e step with a job token lock of its plan via POST /api/v2/workspaces/{id}/actions/lock past the state.write gate with a job-token credential. Confirmed by: CI (make e2e). Check: E2E job token takes its plan lock via POST /api/v2/workspaces/{id}/actions/lock past the state.write gate with a job-token credential.
- [ ] 3.2 Update user docs in the same pull request. Confirmed by: CI (make check). Check: Docs page for state.write gates names job-token and unlocked-write behavior.
- [ ] 3.3 Run usability check and full checks. Confirmed by: CI (make check). Check: make check reports zero findings.
