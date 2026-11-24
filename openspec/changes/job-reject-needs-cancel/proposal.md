---
version: 5
---

Issue: none. The owner decided the rule on 2026-11-15 (option b, relayed by the lead) after a review of #3251 found that a rejection stops a job like a cancel does, with a weaker action.

Depends on: none. The approvals route and `job.Service.Approve` exist on main. The confirmation route of #3251 is not changed.

## Why

**Persona: namespace administrator.** The administrator gives the role `maintainer` to a team. The team expects a person who can stop a job to hold `job:cancel`, and they read the grant list to check it.

The user path this change must make work:

1. **API.** `GET /api/server/namespaces/{namespace}/grants` (`namespaceGrantsPath`, `internal/api/server/access.go:283`) lists who holds `job:cancel` and `job:approve` on a unit.
2. **API.** A person with `job:approve` and without `job:cancel` posts the verdict `reject` to `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals` for a job that another person started.
3. **API.** The answer is 403, and the audit log shows the refusal.
4. **Docs page.** `docs/guides/approval-rules.md` says which action a rejection needs.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A person rejects a job that another person started, and holds `job:approve` but not `job:cancel`. | 403 with code `access.denied` names `job:cancel` and the unit. | `GET /api/server/namespaces/{namespace}/grants` (the grant list; a namespace administrator adds the grant) |
| A person without `job:approve` posts a verdict. | 403 with code `access.denied` names `job:approve`, as today. | `GET /api/server/namespaces/{namespace}/grants` |
| An agent posts a rejection for a person that did not start the job, and the person holds `job:approve` but not `job:cancel`. | 403 with code `access.denied` names `job:cancel`. The rule uses the person, so an agent gets no more than the person has. | `GET /api/server/namespaces/{namespace}/grants` |

Evidence that the gap exists on main (`0d8a9151`):

- A rejection needs `job:approve` only. Proof: `rg -n "ActionJobApprove" pkg/job/service_approve.go` prints `authorizedJob` (`pkg/job/service_approve.go:165`, which calls `authorizedFor` with `ActionJobApprove`, `pkg/job/service_cancel.go:152`), and `checkApproval`, which calls `checkApprover`, runs for `VerdictApprove` only (`pkg/job/service_approve.go:67`).
- The comment on `checkGroups` (`pkg/job/service_approve.go:276`) states the rule today: "any holder of job:approve can reject, because a rejection only stops the change."
- A rejection ends the job in the state `rejected` (`pkg/job/state.go:36`), as a cancel ends it in `canceled`. Both stop the job and both write an audit entry (ADR 103 decision 9). A person with `job:approve` and without `job:cancel` can stop the job of another person. That is the gap.

## What Changes

- A verdict `reject` by a person that is not `Job.StartedBy` needs `job:cancel` on the unit of the job, in addition to `job:approve`.
- A verdict `reject` by the person in `Job.StartedBy` needs `job:approve` only, as today. The test is the same subject test that `checkApprover` uses for the self-approval rule.
- The check runs in `job.Service.Approve` and again in the HTTP handler, so that neither layer relies on the other.
- A refusal names `job:cancel` and writes one audit entry, as the other refusals of the route do.
- The docs page `docs/guides/approval-rules.md` says which action a rejection needs.

Out of scope:

- The confirmation route of #3251. A rejection of a confirmation is made by the person who asked, with the start action (ADR 103 decision 9, row "Confirmation or its rejection"). This change does not touch it. So a `planner` that started a plan job still rejects its own confirmation with `unit:run-plan`.
- The built-in role sets. This change adds no action to a role.
- The expiry of a gate and the cancel route.
- The difference between ADR 128 decision 10 ("`job.Service.Approve` refuses each verdict, `approve` and `reject`, whose actor has an agent reference, with `ErrAgentDenied`") and the code, which allows a rejection by an agent (`pkg/job/service_approve.go:29`, "A rejection by an agent is allowed"; `checkApprover`, which refuses an agent, runs for `VerdictApprove` only). The PM files it as a bug issue. This change applies its rule to the person of the agent, so it holds in both cases.

Depends on: none.

## Usability

Where the path still costs the person time or surprises them:

- An approver who held `job:approve` only could reject before. The refusal now names `job:cancel`. The docs page and the answer say so.
- A person sees the two actions in one grant list, so no second command is needed.

## ADR check

Each quote is copied from `docs/adr/`.

| ADR | Decision lines that apply | Owner | Changed-by chain (followed to the end) | Current rule |
| --- | --- | --- | --- | --- |
| ADR 103 (`0103-job-gates-actions-and-audit.md`) | Decision 8 table: "`job:approve` \| unit \| Approve or reject a job that waits for an approval gate." and "`job:cancel` \| unit \| Cancel a job." Decision 9 table: "Approval or rejection \| the approver, and the agent of a rejection \| `job:approve` \| `change`" and "Confirmation or its rejection \| the person who asked, and the agent of a rejection \| the start action \| `change`". | Records owner decisions | ADR 110 changes decisions 1 and 8; ADR 128 changes decisions 2, 7, 9, 10 and 12; ADR 138 extends 8 and 9; ADR 222 changes decision 2. None changes the two rows quoted, except that ADR 128 removes the agent from the rejection rows (below). | The "Current rule" cell of the INDEX row 103 |
| ADR 128 (`0128-agents-in-the-job-model-and-the-history-model.md`) | Decision 10: "`job.Service.Approve` refuses each verdict, `approve` and `reject`, whose actor has an agent reference, with `ErrAgentDenied`, on each gate." Decision 12: "A `reject` verdict by a person, a cancel and the expiry of a gate still apply." | Not tagged | None changes decisions 10 and 12. | ADR 128 |
| ADR 212 (`0212-planner-role-smtp-settings-fresh-mfa-proxies-restore-audit-and-branding.md`) | Decision 1: "It cannot apply, destroy, approve or read a secret value." (`planner`). | Not tagged | ADR 373 extends decision 1. | ADR 212 with ADR 373 |
| ADR 373 (`0373-job-roles-the-answer-to-an-ended-token-and-the-break-glass-pass.md`) | Decision 1 (owner, 2026-11-14): "`planner` holds `job:read`, `job:read-plans`, `job:read-logs` and `unit:run-plan`. `writer` holds the actions of `planner`. `maintainer` holds the actions of `writer`, `unit:define-steps` and `namespace:manage-runners`. `admin` holds all of them." | Owner-tagged | None. | ADR 373 |

Result: the rule is the owner decision of 2026-11-15, so no conflict stops the change. It narrows the table row "`job:approve` ... Approve or reject" of ADR 103 decision 8: a rejection by a person who did not start the job needs `job:cancel` too. The PM records an amendment of ADR 103 decision 8 with the tag "(owner, 2026-11-15)" and the Changed-by item; this change does not edit the ADR. ADR 373 decision 1 is not touched: this change gives no role a new action.

## Open questions

none

Questions considered and answered:

- Who decides the rule? The owner, on 2026-11-15 (option b). This change implements it.
- Does the requester need `job:cancel` to reject its own job? No. The owner excepted the requester, so a `planner`, which holds `unit:run-plan` and not `job:cancel` (`pkg/access/planner_role_test.go:37`), can reject its own job.
- Which test finds the requester? `a.Principal.Subject == j.StartedBy`, the test of the self-approval rule (`pkg/job/service_approve.go:227`, in `checkApprover`). A user who started the job through an agent is the starter by subject.
- What does an agent do? The rule tests the principal, which is the person that the agent acts for (ADR 100 decision 17). An agent reaches a rejection only as far as its person does. ADR 128 decision 10 refuses an agent verdict, and the code does not yet (see "Out of scope"); the new rule holds either way.
- Does this change a built-in role? No. See the owner question below.

## Owner question outside this change

This question does not block the change, because the rule works without it. The PM puts it in the queue of owner decisions. Nobody decides it provisionally.

- The owner wishes that a role that can start a job can also cancel it. Today only `planner` breaks this: it holds `unit:run-plan` and not `job:cancel`. Should `planner` hold `job:cancel`? That answer changes ADR 212 decision 1 ("It cannot apply, destroy, approve or read a secret value" does not name `job:cancel`, but `pkg/access/planner_role_test.go:37` asserts that it does not hold it) and ADR 373 decision 1 (owner-tagged). It needs an RFC and then an ADR first, and the owner decides.

## What main already does

Each fact names its file, line and function on main (commit `0d8a9151`).

- `Service.Approve` checks the verdict, the read and the approve action, the waiting gates, and then runs `checkApproval` for `VerdictApprove` only. (`pkg/job/service_approve.go:38`, `Approve`)
- `authorizedJob` returns a `*DeniedError` on `job:approve`, and `ErrNotFound` for a caller without `job:read`. (`pkg/job/service_approve.go:165`, `authorizedJob`)
- `checkApprover` refuses an agent, a subject that fills no slot, and the starter. (`pkg/job/service_approve.go:210`, `checkApprover`)
- The route checks `job:approve` and writes an audit entry for a refusal of security. (`internal/api/jobs/approve.go:116`, `approveJob`; `internal/api/jobs/approve.go:245`, `recordApprovalRefusal`)
- `ActionJobCancel` is in the family `data`. (`pkg/access/access.go:226`)
