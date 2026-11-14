## 1. Failing tests

- [ ] 1.1 Add failing gate service tests for out-of-phase, cross-unit, plan-lock allowed, no-phase, cancelled-job, deny-gate, and unlocked-write job writes. Check: go test ./pkg/gate/... -run TestStateWriteJobClaim -count=1
- [ ] 1.2 Add failing adapter tests that a job token without PassedApply writes only the plan lock or lease on each tool path. Check: go test ./internal/api/... -run TestStateWriteJobTokenRefused -count=1

## 2. Implementation

- [ ] 2.1 Add the Claimer source of JobClaim and pass it to Evaluate at state.write, resolved fresh for each call. Check: go test ./pkg/gate/... -count=1
- [ ] 2.2 Wire the Claimer through the Pulumi and TFE-compatible state-write paths including unlocked-write. Check: go test ./internal/api/... -count=1

## 3. Definition of done

- [ ] 3.1 Run e2e step with a runner plan and apply for each tool. Check: make e2e with a runner plan then apply against one stack and one workspace
- [ ] 3.2 Document the refusal, the plan-lock exception, and the log check. Check: docs page for state.write job-token rule renders and each command runs as written
- [ ] 3.3 Pass the definition of done: wiring, route actions, e2e step, docs page, usability check, make check. Check: make check with zero findings
