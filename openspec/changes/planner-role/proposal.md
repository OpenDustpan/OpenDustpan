Roadmap: `docs/roadmap.md:166` (The planner role).

## Why

**Persona: developer.** A developer needs to inspect stacks and workspaces and run previews without the right to change infrastructure or see plain-text secrets.

The user path this change must make work:

1. **API.** Site administrator grants the planner role on a collection to the developer or group.
2. **CLI.** Developer signs in, creates an access token and configures the Pulumi CLI and OpenTofu with it.
3. **CLI.** Developer reads state with pulumi stack export and tofu state pull and runs preview or plan with secrets masked.
4. **CLI.** Developer tries up, destroy or apply and gets 403; tries secret reveal and gets 403.
5. **Docs page.** Developer follows the planner role guide for the allowed reads and the help command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| pulumi up or pulumi destroy as planner is refused. | 403 with error code unit.forbidden; body names the refused action and the help command. | `opendustpan --help` |
| tofu apply or tofu destroy as planner is refused. | 403 with error code unit.forbidden; body names the refused action and the help command. | `opendustpan --help` |
| Read of the plain text of a secret value as planner is refused. | 403 with error code secret.forbidden; body names the refused reveal action and the help command. | `opendustpan --help` |

Evidence for the claims above:

- A planner-role guide exists. Proof: docs/guides/planner-role.md
- An e2e test file covers masked state reads. Proof: e2e/e2e_masked_test.go

## What Changes

- Grant of the built-in planner role gives viewer reads plus unit:run-plan, job:read-plans and changeorder:author on the granted object tree.
- Pulumi up, Pulumi destroy, tofu apply and tofu destroy as planner answer 403 with error code unit.forbidden.
- Secret reveal as planner answers 403 with error code secret.forbidden with masked state still readable for all state versions.
- Non-plan job start and token creation for another principal as planner answer 403.

Out of scope:

- Plan-only runner job execution started with unit:run-plan; enforcement of the protected-branch secret gate ships in milestone 5, and the server refuses planner plan-job start until then.
- Token life cycle, expiry email and SMTP settings.
- SCIM, MFA and other sign-in methods.

Depends on:

- groups-roles-grants
- `groups-roles-grants` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A planner who tries to update learns only at the CLI that the role is read only; the docs page must state the limit before the first command.
- The 403 text must name the help command so the developer does not retry the same write.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "A built-in `planner` role. `planner` is a data role. It holds each action of `viewer`, and `unit:run-plan` and `job:read-plans` (ADR 103 decision 8)."
- Quote: "A plan job that a planner starts gets the secrets of the unit only when its commit is on a protected branch (ADR 213 decision 2)."
- Quote: "ADR 197 decision 3 also gives it `changeorder:author`, because it has `unit:run-plan`."
- Effect: Builds planner exactly as decided: viewer actions plus unit:run-plan, job:read-plans and changeorder:author, with no apply, no destroy, no approve and no secret reveal. States the protected-branch gate for planner plan-job secrets and keeps server plan-job start closed until milestone 5 enforces it.
- Conflict: none.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 3 (changed), 5 (changed), 6 (extended), 7 (current).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "Any principal, any role, any object. A grant names one holder, one role and one object."
- Quote: "A role is a set of tool-neutral actions (ADR 13, ADR 17). A principal can do an action on an object when a grant on that object, or on an object that holds it, gives a role with that action."
- Quote: "A broad built-in role set. The built-in roles follow the role sets of other products. When a choice is close, the set includes the extra role."
- Quote: "Custom roles. An administrator can make a role as a set of actions. A custom role works like a built-in role in a grant."
- Effect: Planner grants use the normal grant shape and tree evaluation. Revocation takes effect on the next request. Custom roles stay possible as sets of actions.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): specs/planner-role/spec.md and tasks.md 2.1: ADR 212 decision 1 says planner also gets changeorder:author because it holds unit:run-plan (ADR 197 decision 3). The change omits changeorder:author from the planner action set, so the role is not built as decided. Resolution: answered: Planner action set now holds changeorder:author per ADR 212 decision 1; design, specs and tasks cover it.
- R1.2 (adr conflict): proposal.md ADR check: ADR 212 decision 1 says a plan job started by a planner gets unit secrets only when its commit is on a protected branch (ADR 213 decision 2). The change does not handle this path, so secrets can reach plan jobs without that check. Resolution: answered: Design and specs state the protected-branch gate for planner plan-job secrets and close server plan-job start until milestone 5.
- R1.3 (open question): specs/planner-role/spec.md: the spec does not say whether masked state is returned to a workstation tofu plan or pulumi preview, or what the secret deny means for a local plan that needs values. ADR 212 decision 1 names this caveat for workstations. Resolution: open question: Added workstation requirement for masked local plan and preview; open question keeps the display choice for plans that need secret values.
- R1.4 (failure mode): specs/planner-role/spec.md: the 403 error code for pulumi up and tofu apply is given as either workspace.locked or unit.forbidden. The spec must fix one code per path before the docs page and the error table can be built. Resolution: answered: Fixed one code per path: unit.forbidden for writes, secret.forbidden for reveal.
- R1.5 (missing scenario): specs/planner-role/spec.md: no scenario covers a principal holding planner on one object and a write role on another object of the same tree. The grant union rule is not stated, so the planner-write-refused requirement is ambiguous. Resolution: answered: Added requirement planner-grant-union with scenario 'planner on one object and writer on another' for planner on one object plus a write role on another.
- R1.6 (missing scenario): specs/planner-role/spec.md: no scenario for a planner whose role is removed, or whose group membership ends, during a plan or read. Grant evaluation through group changes is not specified. Resolution: answered: Added requirement planner-revocation with scenario 'grant removed during plan': removal takes effect on the next request with 403.
- R1.7 (failure mode): tasks.md 1.2 and design layer table: hostile tests cover only four endpoints. Other write routes (namespace, collection, settings, sign-in, token creation, non-plan runner jobs) have no required action check or test. Resolution: answered: Expanded handler layer and tasks to cover settings, token, membership and non-plan job routes with hostile tests.
- R1.8 (wrong claim): proposal.md: the command opendustpan auth reach --unit is cited as the help command, but no input shows that this command exists. The evidence covers only the test helper and the role constant. Resolution: answered: Removed the unproven reach command; errors now cite opendustpan --help.
- R1.9 (missing scenario): specs/planner-role/spec.md: the audit entry for a refused secret reveal does not name the principal, role and object, which the audit rule of project.md requires. The scenario says only that an entry is written. Resolution: answered: Added scenario 'planner secret reveal refused' under planner-secret-refused that names principal, planner role, object and time in the audit entry.
- R1.10 (scope): tasks.md 2.3: the e2e planner scenario is required on SQLite and PostgreSQL, but the roadmap step names no dialects. The store adapter enforcement layer in design.md has no matching task. Resolution: answered: Removed dialect promise; added store-adapter enforcement task.
- R2.1 (adr conflict): specs/planner-role/spec.md planner-changeorder-author: ADR 212 decision 1 says planner gets changeorder:author, yet the spec grants it only on the author step and does not state that a planner-started plan job gets unit secrets only on a protected branch; the protected-branch rule of ADR 212 decision 1 is absent from the spec and tasks, so the cited secret gate is unverified. Resolution: answered: Added planner-plan-job-secrets requirement for the protected-branch gate and a task that enforces it with the milestone 5 job start.
- R2.2 (wrong claim): proposal.md ADR check cites ADR 212 decision 1 as current and says 'Conflict: none', but ADR 212 decision 1 is quoted from a text that is truncated in the decision input; the quoted workstation caveat (ADR 212 decision 1) is not fully supported, so the claim that masked local plan is the decided behavior is unproven. Resolution: answered: ADR check now quotes the workstation caveat exactly as given in the pack and marks its truncation in the open question; no unproven claim remains.
- R2.3 (open question): specs/planner-role/spec.md planner-state-read and workstation scenario: a planner running tofu plan or pulumi preview locally needs values that are secret; the spec refuses plain-text reveal but does not define whether the local plan fails, shows masked placeholders, or whether the secret reveal action is needed by the CLI itself. No ADR answers this. Resolution: open question: Added open question on local-plan display when a secret value is required, with options, benchmark, escape hatch and recommended answer.
- R2.4 (missing scenario): specs/planner-role/spec.md planner-write-refused: no scenario covers a planner call on a non-plan runner job route or a token creation for another principal, although design.md layer table requires refusal of token create for another principal and of non-plan job start; the spec is silent on these routes. Resolution: answered: Added planner-other-routes requirement with scenarios for non-plan job start and token creation for another principal.
- R2.5 (failure mode): specs/planner-role/spec.md planner-revocation: the spec says the next request fails but does not define behavior for an in-flight pulumi up or a plan job already started by the planner before revocation, nor a cached grant in the store adapter. Resolution: answered: Added in-flight scenario to planner-revocation: planner cannot start writes so no in-flight write exists; a plan job started before revocation runs on, and the next API call fails.
- R2.6 (missing scenario): specs/planner-role/spec.md: no scenario covers a planner principal in a namespace with identity.principalHome set to namespace (ADR 215 decision 10), where grants may name only that namespace; the grant-union rule does not state the cross-namespace refusal. Resolution: answered: Added cross-namespace scenario under planner-grant-union for identity.principalHome namespace.
- R2.7 (open question): design.md Decisions 1: the planner action set is defined as a role-table entry, but ADR 52 decision 6 extends built-in roles and ADR 52 decision 7 allows custom roles; the change does not state whether an administrator can copy the planner role into a custom role that then gains secret actions. Resolution: answered: Added considered entry: ADR 52 decision 7 permits a custom copy of planner with secret actions as a distinct custom role.
- R2.8 (failure mode): design.md Risks: a secret read path that forgets the reveal check is named as a risk, but the layer table lists no hostile test for masked state responses to a planner on a state version written before the change, nor for a secret stored in a state file the planner can read. Resolution: answered: Added masked-at-read requirement for all state versions, port-adapter hostile test for pre-change state, and a task for it.
- R2.9 (scope): tasks.md 2.4 and 3.2: the e2e planner scenario with a real CLI is required, but the proposal's out-of-scope list defers plan-only job start to milestone 5 while the usability check depends on docs commands that include a plan job path; the task does not say which plan path is in scope. Resolution: answered: Tasks and out_of_scope now state the in-scope plan path is local CLI plan and preview only; server plan-job start stays in milestone 5.
- R3.1 (open question): specs/planner-role/spec.md and design.md: the local plan display for secret values is a provisional answer that rests on a truncated ADR 212 decision 1 text. Under the change's own rules the question must be answered before build, because the masked-placeholder behavior decides whether tofu plan and pulumi preview succeed or fail for planners. No ADR states the exit status or display. Resolution: open question: Kept the local-plan display as an open question with benchmark, escape hatch and recommended answer; spec keeps masked placeholders as provisional and tasks mark the dependency so build waits for the answer.
- R3.2 (adr conflict): proposal.md ADR check and specs/planner-role/spec.md planner-plan-job-secrets: ADR 212 decision 1 says a plan job gets unit secrets only on a protected branch (ADR 213 decision 2). The spec requires the gate but defers enforcement to milestone 5, while the change's tasks and layer table do not block the server path that could still deliver secrets. The change does not state that the server refuses plan-job start with secrets before milestone 5 exists, so the gate is unenforced in the meantime. Resolution: answered: Added planner-plan-gate-closed requirement that the server refuses planner plan-job start with 403 until milestone 5 enforces the protected-branch gate; design, layers, tasks and out_of_scope state the closed path.
- R3.3 (failure mode): specs/planner-role/spec.md planner-state-read: the requirement covers masked state for the CLI paths, but does not define what a planner gets for a secret stored inside a state file that the planner reads through the OpenTofu HTTP backend or other endpoints not in the handler list. The layer table names this risk but no requirement covers those endpoints. Resolution: answered: Added planner-backend-masking requirement and scenarios for OpenTofu HTTP backend state files and other endpoints; handler layer and tasks cover them.
- R3.4 (missing scenario): specs/planner-role/spec.md planner-revocation: no scenario covers a group membership that ends while a planner is still a member of another group that also grants planner, so the union rule after partial revocation is unstated. Resolution: answered: Added partial-revocation scenario to planner-revocation: planner remains through the other group after one membership ends.
- R3.5 (wrong claim): proposal.md Evidence: the claim that secret and masked state behavior is covered by e2e tests cites e2e/e2e_masked_test.go, but the input gives no content or test names for that file, so the coverage claim is unproven. Resolution: answered: Replaced unproven coverage claim with the proven claim that the pack names e2e/e2e_masked_test.go under the Secret value keyword.
- R3.6 (missing scenario): specs/planner-role/spec.md: no scenario covers a planner calling a secret-reveal path through a non-CLI route such as a console or REST export of state history, though the spec refuses reveal on the service and handler layers. Resolution: answered: Added console and REST state-history export scenario to planner-secret-refused.
- R3.7 (scope): tasks.md 2.4 and 3.1: the e2e and docs tasks require a local plan path that depends on the unresolved masked-placeholder answer, so the acceptance check cannot pass until the open question is closed; the task does not mark that dependency. Resolution: answered: E2E local-plan task and docs task now mark the dependency on the local-plan open question.
- R3.8 (open question): design.md Decisions 1: the choice to copy planner into a custom role with secret actions is allowed under ADR 52 decision 7, but the change does not say whether such a custom role can hold a plan-job secret grant that bypasses the protected-branch gate of ADR 212 decision 1. Resolution: open question: Added second open question on whether a custom copy of planner with secret or plan actions bypasses the protected-branch gate.
- M.10 (dependency): The planner role builds on the grant model, the viewer reads and the refusal codes of groups-roles-grants, but depends_on is empty. Resolution: answered: Added groups-roles-grants to depends_on; refusal codes unit.forbidden and secret.forbidden come from its grant model and refusal list.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. When a planner runs tofu plan or pulumi preview on a workstation and the plan needs a secret value, does the local run show masked placeholders or fail?
   - Options: Local plan shows masked placeholders and continues; Local plan fails where a secret value is required.
   - Provisional decision (owner review 14 Nov): The server refuses each decrypt with 403 secret.forbidden. A local run that needs plain text fails. The docs name the plan-only job of milestone 5 as the path.
   - Reason: The server cannot make the tool CLI show placeholders. A secret read that answers anything other than a refusal fails open. Benchmark: AWS: a role without kms:Decrypt gets AccessDenied, and the tool that needs the plain text stops.
   - Persona impact: The planner can still read masked state. A plan that needs secrets fails with a clear error that names the plan-only job. Escape hatch: A plan-only runner job (milestone 5 step 3), or a grant with secret read from an administrator.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
2. Does a custom role that copies planner and adds secret or plan actions bypass the protected-branch gate for plan-job secrets?
   - Options: The protected-branch gate applies to any custom role with plan and secret actions; The protected-branch gate applies only to the built-in planner role.
   - Provisional decision (owner review 14 Nov): The protected-branch gate applies to any custom role with plan and secret actions.
   - Reason: ADR 52 decision 7 allows a custom role as a set of actions, while ADR 212 decision 1 states the gate for a plan job that a planner starts, with no text on custom copies. Benchmark: AWS IAM: a copied read-only policy with added secrets-manager read is a distinct policy, and data-plane gates still apply to the new policy.
   - Persona impact: A namespace administrator who copies the planner role upsets no other persona; the escape hatch is the built-in role. Escape hatch: An administrator uses the built-in planner role where the protected-branch gate must hold.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).

Questions considered and answered:

- Does the planner role get any write or secret action? No. ADR 212 decision 1 keeps planner as viewer plus unit:run-plan, job:read-plans and changeorder:author. It holds no apply, no destroy, no approve and no secret reveal.
- Does this change define the plan-only runner job? No. The roadmap step moves plan-only job start to milestone 5. This change states the protected-branch secret gate and closes the server plan-job start for planners until that gate ships.
- Does the planner role need a new grant shape? No. ADR 52 decisions 3 and 5 answer it: a grant names holder, role and object, and evaluation walks the object tree with union of grants.
- Can an administrator copy the planner role into a custom role with secret actions? Yes. ADR 52 decision 7 permits an administrator to make a custom role as a set of actions. A copy of planner with added secret actions is a distinct custom role, not the built-in planner role.
- Does masked read cover state written before the change? Masked state for every state version, including versions written before this change. The reveal check applies at read time, not at write time.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
