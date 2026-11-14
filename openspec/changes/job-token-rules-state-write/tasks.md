## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing gate service tests for out-of-phase, cross-unit, plan-lock allowed, no-phase, cancelled-job, deny-gate, lease-held, unlock-other-job, and unlocked-write job writes. Check: go test ./pkg/gate/... -run TestStateWriteJobClaim -count=1
- [ ] 1.2 Add failing adapter tests that a job token without PassedApply writes only the plan lock or lease on each tool path. Check: go test ./internal/api/... -run TestStateWriteJobTokenRefused -count=1

## 2. Implementation

- [ ] 2.1 Reuse the existing job.Claimer source of JobClaim and pass it to Evaluate at state.write, resolved fresh for each call. Check: go test ./pkg/gate/... -count=1
- [ ] 2.2 Wire the Claimer through the Pulumi and TFE-compatible state-write paths including unlocked-write. Check: go test ./internal/api/... -count=1

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run an e2e step with a runner plan and apply for each tool with a job-token credential. Check: make e2e passes the runner plan then apply step for both tools
- [ ] 4.2 Document the refusal, the plan-lock definition, and the log check with a tested command. Check: docs page renders and `kubectl logs -l app=opendustpan --tail=100 | grep "state.write denied"` runs as written
- [ ] 4.3 Pass the definition of done: wiring, route actions, e2e step, docs page, usability check. Check: make check with zero findings
