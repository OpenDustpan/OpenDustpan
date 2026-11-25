## 1. Failing tests first

- [ ] 1.1 SECURITY. Add the failing service table `TestApproveRejectNeedsCancel` in `pkg/job/service_approve_test.go`: another person with `job:approve` and without `job:cancel` (denied), with both (rejected), the requester without `job:cancel` (rejected), a failing authorizer for `job:cancel` (denied), a user who started the job through an agent (requester by subject), and an approval by a person without `job:cancel` (not affected). Each denial asserts `*DeniedError` with `Action` `job:cancel`, and that the job is unchanged.
  - Check: `go test -race ./pkg/job/ -run TestApproveRejectNeedsCancel` (fails on main: the denial cases pass the call)
  - Confirmed by: CI
  - OpenDustpan: `pkg/job/service_approve_test.go`
- [ ] 1.2 SECURITY. Add the failing handler table `TestApproveRouteRejectNeedsCancel` in `internal/api/jobs/approve_test.go` with a service stub that has no rejector check: the same cases, plus the audit entry of a refusal that names `job:cancel` and the unit.
  - Check: `go test -race ./internal/api/jobs/ -run TestApproveRouteRejectNeedsCancel`
  - Confirmed by: CI
  - OpenDustpan: `internal/api/jobs/approve_test.go`

## 2. The rule

- [ ] 2.1 SECURITY. Add `isStarter(j, a)` and `checkRejector(ctx, j, a)` in `pkg/job/service_approve.go`. Make `checkApprover` call `isStarter`. Call `checkRejector` from `Approve` for `VerdictReject`. Comment each redundant check as a defense-in-depth layer.
  - Check: `go test -race ./pkg/job/...`
  - Confirmed by: CI
  - OpenDustpan: `pkg/job/service_approve.go`
- [ ] 2.2 SECURITY. Make `approveJob` (`internal/api/jobs/approve.go:116`) authorize `job:cancel` for a `reject` verdict by a person that is not the starter, and write the refusal entry with `recordApprovalRefusal` (`internal/api/jobs/approve.go:245`). Update the doc comments of `Approve` (`pkg/job/service_approve.go:29`) and `checkGroups` (`pkg/job/service_approve.go:276`) (the line "any holder of job:approve can reject").
  - Check: `go test -race ./internal/api/jobs/...`
  - Confirmed by: CI
  - OpenDustpan: `internal/api/jobs/approve.go`, `pkg/job/service_approve.go`
- [x] 2.3 Mutation check: remove the guard of 2.1 (`checkRejector` in `pkg/job/service_approve.go`) and of 2.2 (the `job:cancel` check in `internal/api/jobs/approve.go`) one at a time, and record that the tests of 1.1 and 1.2 fail. Record the output in the pull request body.
  - Check: the recorded output
  - Confirmed by: the reviewer
  - OpenDustpan: `pkg/job/service_approve.go`, `internal/api/jobs/approve.go` (changed for the check only, then restored)

## 3. Side effects (computed from main)

- [ ] 3.1 Update the docs page `docs/guides/approval-rules.md`: a rejection by another person needs `job:cancel`, the requester is the exception, the error and the command that helps. A test runs each command.
  - Check: `make docs-lint` and the docs command test
  - Confirmed by: CI
  - OpenDustpan: `docs/guides/approval-rules.md`
- [ ] 3.2 Add a step to the e2e suite for the approvals route (`approvalsRoute`). A member has `namespace:read`, `unit:read`, `job:approve` and `job:read`, and no `job:cancel`. The member rejects the job of an administrator. The step expects 403.
  - Check: `make e2e` step for the approvals route
  - Confirmed by: CI
  - OpenDustpan: `e2e/jobs_api_test.go`

## 4. Definition of done

- [ ] 4.1 Usability check: a namespace administrator finds the refusal, the grant list and the grant command from the docs page alone. Record the result in the pull request.
  - Check: a person's walk through, recorded in the pull request body
  - Confirmed by: the reviewer
  - OpenDustpan: none
- [ ] 4.2 Run `make check`, then archive the change with `npx @fission-ai/openspec@1.15.0 archive job-reject-needs-cancel --yes` in the pull request that completes the last task.
  - Check: `make check`
  - Confirmed by: CI
  - OpenDustpan: none
