---
version: 3
---

Roadmap: `docs/roadmap.md:166` (The planner role).

## Why

**Persona: developer.** A developer needs to inspect stacks and workspaces without the right to change infrastructure or see plain-text secrets.

The user path this change must make work:

1. **API.** Site administrator grants the planner role on a collection to the developer or a group.
2. **CLI.** Developer creates an access token for self and configures the Pulumi CLI and OpenTofu with it.
3. **CLI.** Developer reads state with pulumi stack export and tofu state pull with secrets masked.
4. **CLI.** Developer tries up, destroy or apply and gets 403; asks for a secret reveal and gets 403.
5. **Docs page.** Developer follows docs/guides/planner-role.md for the allowed reads.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| pulumi up or pulumi destroy as planner is refused. | 403 with the error code of the refused write action. | `opendustpan --help` |
| tofu apply or tofu destroy as planner is refused. | 403 with the error code of the refused write action. | `opendustpan --help` |
| Read of the plain text of a secret value as planner is refused. | 403 with the error code of the refused reveal action. | `opendustpan --help` |

Evidence for the claims above:

- A planner-role guide exists. Proof: docs/guides/planner-role.md
- An e2e test file covers masked state reads. Proof: e2e/e2e_masked_test.go
- A test of the planner role exists on main. Proof: pkg/access/planner_role_test.go:12
- The reveal route of a stack exists. Proof: route ANY /api/stacks/{}/{}/{}/state/{}/reveal in the pack

## What Changes

- Close the gaps between main and the planner role: update operations as planner answer 403 and write an audit entry.
- Secret reveal as planner answers 403; masked state stays readable.
- Non-plan job start and token creation for another principal answer 403.
- No new route: the checks use the existing routes under /api/stacks/{}/{}/{}/state/{}/reveal, /api/stacks/{}/{}/{}/state/{}/outputs and /api/server/caller/tokens.

Out of scope:

- Plan-only runner job with unit:run-plan and the protected-branch secret rule (milestone 5).
- Token life cycle, expiry email and SMTP settings.
- SCIM, MFA and other sign-in methods.

Depends on:

- groups-roles-grants
- `groups-roles-grants` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The docs page states the read-only limit before the first command.
- Each 403 names the help command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TestPlannerRole covers the planner role. (`pkg/access/planner_role_test.go:12`)
- The audit log defines the read-access kind. (`pkg/audit/audit.go:58`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "A built-in `planner` role. `planner` is a data role. It holds each action of `viewer`, and `unit:run-plan` and `job:read-plans` (ADR 103 decision 8)."
- Quote: "ADR 197 decision 3 also gives it `changeorder:author`, because it has `unit:run-plan`."
- Effect: Planner is viewer plus unit:run-plan and job:read-plans, with no apply, destroy, approve or secret reveal.
- Conflict: none.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "Any principal, any role, any object. A grant names one holder, one role and one object."
- Quote: "Custom roles. An administrator can make a role as a set of actions. A custom role works like a built-in role in a grant."
- Effect: Planner grants use the normal grant shape.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph; a code check fails: threat 1 (A planner escalates a read grant to a wr): no guard and hostile test for encode or contain, limit; a code check fails: contract method CheckAccess: no requirement or scenario names it.

Questions considered and answered:

- Does the planner role get any write or secret action? No. ADR 212 decision 1 gives no apply, destroy, approve or secret reveal.
- Does this change define the plan-only runner job? No. The roadmap moves it to milestone 5.
- Does the planner role need a new grant shape? No. ADR 52 decision 3 answers it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
