## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing gate service tests for out-of-phase, cross-unit, plan-lock, no-phase, cancelled-job, deny-gate and unlocked-write cases in the new file pkg/gate/statewrite_jobclaim_test.go. Check: go test ./pkg/gate/... -run TestStateWriteJobClaim -count=1
- [ ] 1.2 Write failing subject source tests in the new file pkg/dustpan/jobclaim_test.go (the inner source is `principalSubjects` at pkg/dustpan/stateguard.go:395): a job that resolves gives the claim; an unknown job, an empty job ID and a terminal job each give identity.ErrDenied; a second call after a phase change gives the new phase. Check: go test ./pkg/dustpan/... -run TestJobClaimSubjects -count=1
- [x] 1.3 Write route-level tests for the routes that call `GuardStateWrite` on main (Pulumi import, restore, cancel, delete and rename; TFE lock, force unlock, rollback and delete): an apply-phase job token on the unit of its job is allowed, and the lock of its plan is allowed (these fail on main, because `principalSubjects.Subject` gives no job and the gate refuses each job-token write); a plan-only job token, a token of another unit and a route unit other than the job's unit are refused with 403, each as identity.ErrDenied through the state.write gate with an audit entry. Put the Pulumi cases in the new file internal/api/stategate/jobtoken_pulumi_test.go and the TFE cases in the new file internal/api/stategate/jobtoken_tfe_test.go. Add a test in the new file pkg/stack/preview_no_state_test.go that pins that a preview or a dry run writes no journal batch and no checkpoint (it passes on main by design). Check: go test ./internal/api/stategate/... ./pkg/stack/... -run 'JobToken|PreviewNoState' -count=1

## 2. Implementation

- [ ] 2.1 Add the new file pkg/dustpan/jobclaim.go with `jobClaimSubjects`: it wraps the inner `gate.SubjectSource` and, for `gate.CredentialJobToken`, reads the job with `job.Store.Get` by `Principal.JobID` and sets `Subject.Job` (JobClaim is at pkg/gate/service.go:23). Check: go test ./pkg/dustpan/... ./pkg/gate/... -count=1
- [ ] 2.2 In pkg/dustpan/stateguard.go, where `gate.NewService` gets `Subjects: principalSubjects{...}` (line 128), wrap it with `newJobClaimSubjects` using the job store. `GuardStateWrite` at pkg/gate/statewrite.go:66 then sees the claim on each route that calls it on main. Check: go test ./internal/api/... ./pkg/dustpan/... -count=1

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: the change adds no route and no field, so `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` leaves the golden file and the clients unchanged.

## 4. Definition of done

- [ ] 4.1 Add an e2e step in the NEW file e2e/job_token_state_write_test.go for a runner plan then apply on both tools, using the job token credential on the routes that call the guard on main. BLOCKED by add-job-session-paths-runner-token-issuer, which issues the job token. Check: make e2e.
- [x] 4.2 Document the refusal and the plan lock on the state.write docs page under docs/guides/. Check: make docs-lint
- [ ] 4.3 Wiring, route actions, docs page and usability check done. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
