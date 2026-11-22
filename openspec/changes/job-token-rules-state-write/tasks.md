## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing gate service tests for out-of-phase, cross-unit, plan-lock, no-phase, cancelled-job, deny-gate and unlocked-write cases in the new file pkg/gate/statewrite_jobclaim_test.go. Check: go test ./pkg/gate/... -run TestStateWriteJobClaim -count=1
- [ ] 1.2 Write failing subject source tests in the new file pkg/dustpan/jobclaim_test.go (the inner source is `principalSubjects` at pkg/dustpan/stateguard.go:395): a job that resolves gives the claim; an unknown job, an empty job ID and a terminal job each give identity.ErrDenied; a second call after a phase change gives the new phase. Check: go test ./pkg/dustpan/... -run TestJobClaimSubjects -count=1
- [ ] 1.3 Hostile tests for a plan-only job token (from the work of PR #2940, kept under this change): a Pulumi update create that saves state (not a preview, not a dry run) and an update start of such a job are refused; a TFE state-version create and an OpenTofu HTTP backend state write of such a job are refused; a token of another unit and a route unit other than the job's unit are refused. Each refusal is identity.ErrDenied through the state.write gate and writes an audit entry. Put the Pulumi cases in the new file internal/api/stategate/jobtoken_pulumi_test.go and the OpenTofu cases in the new file internal/api/stategate/jobtoken_tofu_test.go. Add a test in the new file pkg/stack/preview_no_state_test.go that pins that a preview or a dry run writes no journal batch and no checkpoint. Check: the new tests fail on main, because `principalSubjects.Subject` at pkg/dustpan/stateguard.go:402 sets no job and the NEW `jobClaimSubjects` does not exist

## 2. Implementation

- [ ] 2.1 Add the new file pkg/dustpan/jobclaim.go with `jobClaimSubjects`: it wraps the inner `gate.SubjectSource` and, for `gate.CredentialJobToken`, reads the job with `job.Store.Get` by `Principal.JobID` and sets `Subject.Job` (JobClaim is at pkg/gate/service.go:23). Check: go test ./pkg/dustpan/... ./pkg/gate/... -count=1
- [ ] 2.2 In pkg/dustpan/stateguard.go, where `gate.NewService` gets `Subjects: principalSubjects{...}` (line 128), wrap it with `newJobClaimSubjects` using the job store. `GuardStateWrite` at pkg/gate/statewrite.go:66 then sees the claim on the Pulumi and TFE-compatible write routes. Check: go test ./internal/api/... ./pkg/dustpan/... -count=1

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step in the NEW file e2e/job_token_state_write_test.go for a runner plan then apply on both tools, using the job token credential on the Pulumi update routes and the TFE state-version routes. BLOCKED by add-job-session-paths-runner-token-issuer, which issues the job token. Check: make e2e.
- [ ] 4.2 Document the refusal and the plan lock on the state.write docs page under docs/guides/. Check: make docs-lint
- [ ] 4.3 Wiring, route actions, docs page and usability check done. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
