Roadmap: `docs/roadmap.md:269` (The planner role).

## Why

**Persona: developer.** A developer with the planner role tries to check a change with a plan without the right to change production. The server runs the plan, shows the masked plan, and refuses the apply with 403.

The user path this change must make work:

1. **API.** Start a plan job for a stack and a workspace through the jobs API.
2. **API.** Read the masked plan output of the finished plan job.
3. **API.** Attempt an apply job as the same principal and see 403.
4. **Docs page.** Follow the docs page for the planner role.
5. **E2e.** Run the e2e step with the real runner and real CLI.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply job as planner is refused. | 403 with error code job.forbidden and the job is not created. The text names the missing action and the role that holds it. | `curl -X POST /api/dustpan/v1/namespaces/{namespace}/jobs` |
| Plan job as planner fails to start. | 201 with the job in queued state and a link to the masked plan output. | `curl -X POST /api/dustpan/v1/namespaces/{namespace}/jobs` |

Evidence for the claims above:

- The planner role exists in code as an access role. Proof: pkg/access/roles.go:25: RolePlanner = "planner"
- No e2e step in the pack covers a planner apply job. Proof: Context pack evidence for Apply job: e2e [], e2e_count 0

## What Changes

- A principal with only the planner role starts plan-only jobs for stacks and workspaces.
- An apply or destroy job of the same principal gives 403 and creates no job with error code job.forbidden.
- A planner plan job reads branch protection of the named commit at job start and runs masked with no secret values unless that commit is on a protected branch.

Out of scope:

- SMTP settings, fresh MFA, trusted proxies, restore audit, branding preset and settings API of ADR 212 decisions 2 to 7.
- Runner join, claim and log streaming.
- Gate and approval checks on the job.

Depends on:

- add-job-service-routes-start
- job-service-unit-settings-pool
- git-host-webhook-intake-post
- open-profile-serve-open-sessions
- `job-service-unit-settings-pool` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)
- `open-profile-serve-open-sessions` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A planner who needs an apply must ask another person; the 403 text names who can apply.

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

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): ADR 212 decision 1 says a plan job that a planner starts gets unit secrets only when its commit is on a protected branch (ADR 213 decision 2), and that ADR 197 decision 3 gives the planner changeorder:author. The proposal says 'Open questions: none' and lists the secrets question as answered, but design.md and the spec never map the protected-branch gate or changeorder:author to any requirement, layer or test. Resolution: answered: Protected-branch gate and changeorder:author now map to requirements, layers and tests.
- R1.2 (missing scenario): The spec has no scenario for a planner plan job on a commit that is not on a protected branch, and no scenario for the secret handling of a plan job. The proposal promises the masked plan, but the tests in tasks.md do not check secret-free output for an unprotected commit. Resolution: answered: Added secrets-by-branch requirement with unprotected, protected, unreadable and no-secrets scenarios and masking tests.
- R1.3 (missing scenario): Errors in the user path are not specified in the spec. The proposal names job.forbidden with a text that names the missing action, but the spec only says 403 and no job. Neither the error code nor the text requirement is tested. Resolution: answered: Added refusal-shape requirement with error code job.forbidden and missing-action text plus scenario and test.
- R1.4 (failure mode): Design Risks say a workstation plan reads state with state actions outside this check, which is the ADR 212 decision 1 caveat. The change does not say what it does about it, and the docs page task does not require a warning in the docs. Resolution: answered: Risks state the docs warning; docs task requires the workstation caveat warning.
- R1.5 (missing scenario): The spec does not cover a destroy job for a planner. Design decision 1 maps destroy to its write action, but no scenario tests that a planner gets 403 for destroy. Resolution: answered: Added destroy refusal scenario and test alongside apply.
- R1.6 (failure mode): The design does not cover a race or a restart between the operation check and the queue insert, and it does not say how the job record is written if the audit write fails. Decision 2 requires the audit entry, but no failure rule is given for it. Resolution: answered: Added write-order decision: check plus queue in one transaction, audit after commit as non-blocking record with failure rule.
- R1.7 (open question): The proposal lists 'Open questions: none', but the operation-to-action mapping for an unknown or new operation has no defined behavior. Design says a new operation cannot skip the mapping, but it does not say whether the server refuses unknown operations. Resolution: answered: Added unknown-operation requirement that fails closed with 400 and creates no job.
- R1.8 (wrong claim): The proposal's ADR check says decision 1 is not owner-tagged and that planner access is current. It also omits the ADR 197 decision 3 changeorder:author grant that decision 1 names, so the ADR check is incomplete. Resolution: answered: ADR check now quotes both planner sentences including protected branch and changeorder:author.
- R1.9 (scope): The proposal says the change makes 'apply job' refused, but the tasks put a docs page and e2e step in scope without naming the destroy path. Scope against the roadmap line is partial because destroy is named in ADR 212 but not tested. Resolution: answered: Scope names plan, apply, destroy and unknown operations as in scope; out_of_scope lists excluded ADR 212 and runner work.
- R2.1 (adr conflict): ADR 212 decision 1 says a planner plan job gets unit secrets only when its commit is on a protected branch, and otherwise the plan runs masked. The spec Secrets-by-branch requirement says protected commits open unit secrets for the run, but design decision 2 and the layer table say the job opens unit secrets only on a protected branch, and the spec does not state the branch rule as a gate on the principal's commit; the spec and design also do not check that the commit's branch protection is read at job start, so the gate may be applied to the wrong commit. Resolution: answered: Secrets requirement now reads branch protection of the commit named at job start and gates secrets on that read; design and layers state the same read point.
- R2.2 (open question): ADR 212 decision 1 says a planner can read the masked plan through job:read-plans, but the spec and tasks have no requirement or scenario for reading the plan output, so the user path step 2 (read masked plan output) is not built or tested. Resolution: answered: Added masked plan read requirement through job:read-plans with scenarios and tests, covered in user path step 2.
- R2.3 (missing scenario): The spec does not state the changeorder:author grant that ADR 212 decision 1 and ADR 197 decision 3 give a planner, and no task or test checks that a planner holds it or that it is a stored grant rather than an implicit one. Resolution: answered: Added changeorder:author stored-grant requirement with reach test.
- R2.4 (failure mode): Design decision 3 says the operation check and queue insert run in one store transaction and the audit entry is written after commit, but the spec has no requirement for audit failure, restart between commit and audit, or a concurrent grant change between check and insert. Resolution: answered: Added audit follow-up requirement: audit failure never rolls back the decision; restart between commit and audit leaves the job queued with a missing entry noted in the docs.
- R2.5 (missing scenario): The spec does not cover a planner plan job whose unit has no secrets, or a plan job whose branch protection cannot be read (outage of the git host), which should fail closed to masked. Resolution: answered: Added no-secrets unit and unreadable branch-check scenarios that both run masked.
- R2.6 (failure mode): The layer table requires the service to recheck branch protection before opening secrets, but no failure case is given for a branch check that errors or times out. Resolution: answered: Branch check error and timeout fail closed to masked run with no secret value.
- R2.7 (scope): The roadmap step covers only plan-only and apply refusal, but the change adds destroy refusal, unknown-operation refusal, secrets-by-branch and changeorder:author, and the proposal says the destroy path is in scope while the tasks do not include a docs or e2e step for destroy. Resolution: answered: Kept destroy and unknown-operation refusals as the 403 and 400 edges of the same operation check; docs and e2e tasks now cover destroy explicitly.
- R2.8 (wrong claim): The proposal states the error table command for a plan job as a list call, and the apply error command reads one job, which does not show the missing action or who can apply; the claim that the 403 text names who can apply is not in the spec, which says only that the body names the missing action. Resolution: answered: Fixed both error commands to POST jobs; refusal-shape requirement now names the missing action and the role that holds it.
- R2.9 (open question): The spec does not state how a planner's stored grants are checked against the port adapter for apply and destroy when a grant is later removed mid-job; the layer table asserts denial only at start. Resolution: answered: Added grant-timing requirement: checks at start and at each plan read; removal after start revokes later reads and does not retract the queued job.
- M.2 (dependency): This change writes the 'planner-role' capability again beside the existing change 'planner-role' and has no dependencies. It needs the job start route, the role mapping, the forge branch-protection read and the profile open. Resolution: answered: This change is a delta on the existing planner-role capability, not a second definition; it adds only the milestone 5 job-operation mapping, refusals, secrets gating and reads. It now depends on add-job-service-routes-start for the jobs route, job-service-unit-settings-pool for the start inputs, git-host-webhook-intake-post for the branch-protection read, and open-profile-serve-open-sessions for the profile open.

## Open questions

none

Questions considered and answered:

- What can a planner do and not do? ADR 212 decision 1 answers it: viewer plus unit:run-plan and job:read-plans; no apply, destroy, approve or secret read; plus changeorder:author by ADR 197 decision 3.
- Do planner plan jobs get unit secrets? ADR 212 decision 1 answers it: the server reads branch protection of the commit named at job start; only a commit on a protected branch opens unit secrets; an unreadable check fails closed to masked.
- What about the workstation caveat? It stays in scope as a docs warning only: the role limits what the server runs and records, workstation reads use state actions. No behavior change here.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
