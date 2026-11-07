# M5-13a: record approvals of a job (job.Service.Approve and its route)

- Class: SECURITY (authorization of an apply, separation of duties)
- Routing: Sonnet author, Opus review
- Branch: `m5-13a-job-approve`
- Conflict group: `job-approve`
- Persona path: P5 runner apply, step `gate_waits_for_two` (#2610); milestone 5
  exit test step 6.
- Depends on: none to build and test with plan jobs and store-made jobs. The
  P5 end-to-end path also needs M5-8b.

## Facts checked at main 9e96ae30c8c3

- `pkg/job/service.go:330`: `(*service).Approve` returns `ErrUnsupported`
  ("this version does not do this yet"). `Confirm` (line 336) too.
- `pkg/job/store.go:411-426`: the contract of `Service.Approve`: errors
  `ErrTransition`, `ErrAgentDenied` (an agent can record a rejection),
  `ErrSelfApproval`, `ErrPlanChanged`, `ErrPlanStale`, `ErrFinished`; only gates
  of type `GateApproval`.
- `pkg/job/store.go:47`: `Change.Approval *Approval`; the shared store applies it
  (`pkg/store/internal/jobstore/events.go:16`).
- `pkg/job/gate.go`: `Verdict` (163), `Approval` (172), `GateStatus` (207),
  `Gate.Status(approvals, startedBy, policy)` (236), `GatesStatus` (295).
- `pkg/job/artifact.go`: `PlanBinding` (128), `CheckPlan` (147).
- `pkg/job/state.go`: `EventApproved` -> `queued`, `EventRejected` -> `rejected`
  from `awaiting-approval`.
- `pkg/job/job.go:345`: `Actor` with `Principal`, `Agent`, `Mode`,
  `ConfirmAuth`, `AuthAt`. `ErrSelfApproval`, `ErrPlanChanged`, `ErrPlanStale`
  at lines 90-98.
- `pkg/job/actions.go:49`: `ActionJobApprove`.
- `internal/api/jobs/routes.go`: `jobPath = jobsPath + "/{job_id}"`; no approval
  route. `docs/reference/jobs-api.md:10` says the API does not approve yet.
- ADR 191 decision 6 (a new plan resets approvals), ADR 212 decision 3 (the
  approval of a `destroy` needs a fresh MFA step), ADR 225 (approval rules),
  ADR 222 to 230 (gates).

## Files it may touch

- `pkg/job/service.go` (remove the `Approve` stub only)
- `pkg/job/service_approve.go` (new) and `pkg/job/service_approve_test.go` (new)
- `pkg/job/jobtest/` (new file `approve.go` with conformance cases; register them in `jobtest.go`)
- `internal/api/jobs/routes.go` (one route row), `internal/api/jobs/approve.go` (new) and its test
- `openapi.json`, `clients/typescript`, `internal/errcode` catalog entries if a new code is needed (by the generators)
- `docs/reference/jobs-api.md`, `docs/guides/approval-rules.md`

## Files it must not touch

- `pkg/job/service_start.go`, `pkg/dustpan/jobservice.go` (M5-8a and M5-8b own them)
- `Confirm` (a later item), the store adapters (the store already applies `Change.Approval`)

## Change

1. `Approve(ctx, a, id, v, plan, comment)`, in this order, each check fail closed:
   1. Validate `v` (allowlist of the two verdicts) and `comment` (length limit,
      no control characters) at the boundary.
   2. Get the job. Authorize `ActionJobApprove` on `j.Spec.Unit`; a denial of a
      job the principal cannot read is `ErrNotFound`, as `Get` does.
   3. `ErrFinished` for an ended job; `ErrTransition` when the state is not
      `awaiting-approval` or the current gate is not `GateApproval`.
   4. An agent that approves: `ErrAgentDenied`. An agent can reject.
   5. Self approval: the starter, also through an agent, is refused with
      `ErrSelfApproval` unless the gate allows self.
   6. Plan binding: `CheckPlan` with the stored plans; `ErrPlanChanged`,
      `ErrPlanStale`.
   7. Destroy: an approval of a job with operation `destroy` needs
      `a.ConfirmAuth == ConfirmAuthMFA` and a fresh `a.AuthAt`; else
      `ErrAuthStale`. Comment as defense in depth if the route also checks.
   8. A second approval by the same principal counts once (`Gate.Status`).
2. Write one `Change` with the `Approval`, and `EventApproved` when
   `GatesStatus` says open, `EventRejected` on a rejection; append one audit
   entry in the same transaction.
3. Route `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals`,
   action `job:approve` on the unit of the job, body
   `{"verdict","planBinding","comment"}`, strict decode (`internal/textbody`).
   The route test lists it with its action.

## Acceptance

- Unit tests in `pkg/job`, one hostile test for each layer, alone:
  verdict `"approve; drop"`, comment with `\r\n` and over the limit; a
  principal with no `job:approve` (fake authorizer); job in `queued`;
  agent approval; starter approval with and without an agent; plan binding of
  another plan; stale plan; destroy with no MFA, with old MFA; authorizer
  error (fail closed, nothing written, denial audited).
- Two distinct approvers: one approval leaves `awaiting-approval`; the second
  moves to `queued`. A rejection ends `rejected`.
- Conformance cases in `jobtest` pass on the in-memory store, SQLite and PostgreSQL.
- Handler test: 403 / 404 / 409 / 422 mapping, unknown field refused.
- `docs/reference/jobs-api.md` documents the route; line 10 no longer says the
  API does not approve. `docs/guides/approval-rules.md` shows the call.
- `make check` zero findings.
