---
version: 2
---

Roadmap: `docs/roadmap.md:269` (The planner role).

## Why

**Persona: developer.** A developer with the planner role tries to check a change with a plan without the right to change production. The server runs the plan, shows the masked plan, and refuses the apply with 403.

The user path this change must make work:

1. **API.** Start a plan job for a stack and a workspace through the jobs API.
2. **API.** Read the masked plan output of the finished plan job.
3. **API.** Attempt an apply job as the same principal and see 403.
4. **Docs page.** Follow the docs page for the planner role.
5. **E2e.** Run the e2e step with the real runner and real CLI, or see the BLOCKED note in the tasks.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply job as planner is refused. | 403 with error code job.forbidden and the job is not created. The text names the missing action and the action that allows an apply. | `curl -X POST /api/dustpan/v1/namespaces/{namespace}/jobs` |
| Job with unknown operation is refused. | 400 with error code job.invalid-operation and no job is created. The text names the known operations plan, apply and destroy. | `curl -X POST /api/dustpan/v1/namespaces/{namespace}/jobs` |
| Plan job without the planner grant is refused. | 403 with error code job.forbidden and no job is created. The text names the missing unit:run-plan action on the unit. | `curl -X POST /api/dustpan/v1/namespaces/{namespace}/jobs` |

Evidence for the claims above:

- No e2e step in the pack covers a planner apply job. Proof: Context pack evidence for Apply job: e2e [], e2e_count 0
- The planner grant test covers unit:run-plan for a stored person. Proof: pkg/access/namespace_service_grant_test.go:51: TestUnitRunPlanRoleForAStoredPerson

## What Changes

- A principal with only the planner role starts plan-only jobs for stacks and workspaces.
- An apply or destroy job of the same principal gives 403 and creates no job with error code job.forbidden.
- A planner plan job pins the commit SHA from the request, reads branch protection of that SHA at job start, and runs masked with no secret values unless that SHA is on a protected branch.

Out of scope:

- SMTP settings, fresh MFA, trusted proxies, restore audit, branding preset and settings API of ADR 212 decisions 2 to 7.
- Runner join, claim and log streaming.
- Gate and approval checks on the job.

Depends on:

- add-job-service-routes-start-1
- job-service-unit-settings-pool
- open-profile-serve-open-sessions
- `job-service-unit-settings-pool` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)
- `open-profile-serve-open-sessions` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A planner who needs an apply must ask another person; the 403 text names the action that allows it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TestPlannerRole covers the planner role in access checks. (`pkg/access/planner_role_test.go:12`)
- TestUnitRunPlanRoleForAStoredPerson covers unit:run-plan for a stored person. (`pkg/access/namespace_service_grant_test.go:51`)
- The job service holds a planner for fixed steps. (`pkg/job/service.go:136`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "A built-in `planner` role. `planner` is a data role. It holds each action of `viewer`, and `unit:run-plan` and `job:read-plans` (ADR 103 decision 8). So a planner can start a runner job that only plans, and can read the masked plan. It cannot apply, destroy, approve or read a secret value."
- Quote: "A plan job that a planner starts gets the secrets of the unit only when its commit is on a protected branch (ADR 213 decision 2). ADR 197 decision 3 also gives it `changeorder:author`, because it has `unit:run-plan`."
- Effect: The change implements the planner data role as decided: viewer plus unit:run-plan and job:read-plans, no apply, destroy, approve or secret read; planner plan jobs get unit secrets only when the commit named at job start is on a protected branch; planner also holds changeorder:author.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What can a planner do and not do? ADR 212 decision 1 answers it: viewer plus unit:run-plan and job:read-plans; no apply, destroy, approve or secret read; plus changeorder:author by ADR 197 decision 3.
- Do planner plan jobs get unit secrets? ADR 212 decision 1 answers it: only a commit on a protected branch opens unit secrets; an unreadable check fails closed to masked.
- What about the workstation caveat? It stays in scope as a docs warning only: the role limits what the server runs and records, workstation reads use state actions. No behavior change here.
- What stays open for the gate? Grant timing, commit pinning, no-commit default, unknown operation and audit order are answered in the requirements; no open question stays.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
