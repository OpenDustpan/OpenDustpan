## Context

A rejection stops a job, as a cancel does. Today the route needs `job:approve` for both verdicts, so a person that cannot cancel can stop the job of another person. The owner decided on 2026-11-15 that a rejection by a person other than the requester needs `job:cancel` too.

## Decisions

1. **One function decides.** A new unexported function `checkRejector(ctx, j, a)` in `pkg/job/service_approve.go` returns nil when `a.Principal.Subject == j.StartedBy`, else it authorizes `ActionJobCancel` on `j.Spec.Unit`. `Service.Approve` calls it for `VerdictReject` after `authorizedJob`. A denial is a `*DeniedError` with `Action: ActionJobCancel`, and an error of the authorizer is a denial too (fail closed), as `authorizedJob` does.
2. **The subject is the test.** The same comparison as the self-approval rule (`checkApprover`), so a user who started the job through an agent is the requester by subject. One helper `isStarter(j, a)` serves both, so the two rules cannot disagree.
3. **Two layers.** The handler `approveJob` also authorizes `job:cancel` for a `reject` verdict before it calls the service, and `recordApprovalRefusal` writes the audit entry. The handler layer is defense in depth: the service check stays so that another caller cannot skip it, and the handler check stays so that a service with a wrong authorizer cannot widen the route. Each has its own test.
4. **No new action, no new role set.** The rule uses `job:cancel`, which exists. No role changes.
5. **The check order.** Boundary check of the verdict, then `job:read` (a caller that cannot read gets 404), then `job:approve`, then, for a rejection, the rejector check, then the state and the gates. A refusal writes an audit entry and no job change.

Why no structure removes the need for the check: the verdict is data of one route, and the action set of a route is fixed by the route table. A second route for rejection would split one decision in two and need its own gates. The guard and its mutation test take the place of a structure.
6. **The e2e step checks the documented call.** The docs page stays on the pending list of docs commands. The docs runner has no server and no access token, so a page on the checked list would only carry skip markers and run nothing. The e2e step `approvalsRejectNeedsCancel` in `e2e/jobs_api_test.go` runs the grant list and the refused rejection against a real server. This is what the page shows. The change edits no list of docs commands.

## Test plan

| Layer | Hostile case | Test fails when the guard is removed |
| --- | --- | --- |
| Service | A person with `job:approve` and without `job:cancel` rejects the job of another person. | Yes, the call succeeds without the guard. |
| Service | The authorizer fails for `job:cancel`. | Yes, the call succeeds without the fail-closed branch. |
| Service | The requester rejects its own job with `job:approve` and without `job:cancel`. | No guard is involved. The test pins the exception. |
| Service | A person that holds both actions rejects. | No guard is involved. The test pins the pass. |
| Handler | The same four cases through `POST .../jobs/{job_id}/approvals` with a service that has no rejector check. | Yes. |
| Audit | A refusal writes one entry that names `job:cancel` and the unit, and no job event. | Yes. |
| Role | `planner` rejects its own job with `job:approve` by a custom grant. (read only) `pkg/access/planner_role_test.go` (line 37) still asserts `job:cancel` is false. | No guard is involved. |
| E2E | A person with `job:approve` only lists the grants and rejects the job of another person, on a real server. | Yes, the rejection succeeds without the guard. |
