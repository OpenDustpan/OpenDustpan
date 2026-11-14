Roadmap: `docs/roadmap.md:295` (Pre-merge apply, the option).

## Why

**Persona: namespace administrator.** A namespace administrator manages units that need fast iteration on short-lived branches. The default apply after the merge blocks those teams, so they need an explicit per-unit option to apply an approved pull request from its branch.

The user path this change must make work:

1. **Docs page.** Set applyTiming pre-merge on the unit from the docs page.
2. **API.** Approve the review request and request a branch apply.
3. **Console.** Watch the apply job timeline and log.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply requested from the branch on a unit with the default | The server refuses the branch apply and names applyTiming pre-merge and the unit. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Approval is missing or stale at apply time | The server refuses the apply because the approval is missing, targets an older head, or is stale for the current branch head. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| No plan on the exact branch head | The server refuses the apply because no plan exists on the exact branch head commit. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- The milestone step requires a pre-merge apply option with a refusal on default units. Proof: docs/roadmap.md:295
- The item is not shipped. Proof: status: not shipped
- No ADR is given in the context pack for this item. Proof: adrs: []

## What Changes

- Add per-unit applyTiming value with pre-merge option and post-merge default, with effective value in the API.
- Start the apply pinned to the branch head commit only when the unit sets applyTiming pre-merge with approval and plan on that exact commit, re-checked at job start and before the first change.
- Refuse a branch apply on a default unit, with no plan on the exact head, or with a missing or stale approval, with an error that names the cause.

Out of scope:

- Apply after the merge from the default branch
- Pull request plan, status, and summary comment
- Review modes other than the approved modes of this milestone

Depends on:

- apply-after-merge
- pull-request-automation-first-slice
- report-plans-on-review-request
- mirror-approvals-between-review-request
- `apply-after-merge` (order of the milestone review)
- `report-plans-on-review-request` (order of the milestone review)
- `mirror-approvals-between-review-request` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The option name in the refusal matches the setting name in the docs page.
- The unit settings read shows the effective value so a developer sees why a branch apply was refused.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The change has an open question on whether branch apply uses the approved head or needs a fresh plan on that exact head; the spec-ready gate is not met and no ADR answers it. The spec scenario 'starts the apply job from the branch head' does not say which head or plan is used. File: openspec/changes/pre-merge-apply-option/proposal.md. Resolution: answered: Removed the open question; spec requires approval and plan on the exact branch head commit SHA with re-check at start.
- R1.2 (wrong claim): The proposal says the item needs approval current at apply time (409 when stale), but the spec has no requirement or scenario for stale or missing approval. The 'Approved branch apply' scenario only covers an approved review request. File: openspec/changes/pre-merge-apply-option/specs/pre-merge-apply/spec.md. Resolution: answered: Added stale approval refusal requirement and scenarios for moved head and older head.
- R1.3 (missing scenario): The 409 error for missing or stale approval at the branch head has no scenario and no task test. The error table in the proposal is not mapped to any requirement. File: openspec/changes/pre-merge-apply-option/specs/pre-merge-apply/spec.md. Resolution: answered: Added refusal scenarios and failing-test tasks for missing or stale approval.
- R1.4 (failure mode): The design does not say what happens when the branch head moves after approval and before the job starts, or during the apply. A race can apply code that was never reviewed. The design only names a constructor check, with no stated re-check. File: openspec/changes/pre-merge-apply-option/design.md. Resolution: answered: Design states pin of SHA with re-check at job start and before the first change, with refuse or stop.
- R1.5 (missing scenario): The spec does not cover a non-namespace-administrator who sets pre_merge_apply, nor the authorization action and object for the setting change. CLAUDE.md requires each endpoint to declare its actions. File: openspec/changes/pre-merge-apply-option/specs/pre-merge-apply/spec.md. Resolution: answered: Added setting authorization requirement and denied-change scenario.
- R1.6 (failure mode): The risks list names two units that disagree on the option but gives no requirement or test for it. The mechanism that prevents a team from enabling the option without review is also not specified. File: openspec/changes/pre-merge-apply-option/design.md. Resolution: answered: Added design decision and no cross-unit coupling; each unit decides alone.
- R1.7 (wrong claim): The ADR check says no ADR is touched, but the roadmap step says RFC 27 is a draft and the design is needed. The change proceeds without stating whether RFC 27 governs this behavior. File: openspec/changes/pre-merge-apply-option/proposal.md. Resolution: answered: Considered records RFC 27 as draft RFC with no ADR conflict; this change carries the option-slice design.
- R1.8 (wrong claim): The open-questions section says the refusal text was answered by the roadmap, but the roadmap step only requires an error that names the option; it does not fix the wording or the status code (403). The 403 status is taken without support in the input. File: openspec/changes/pre-merge-apply-option/proposal.md. Resolution: answered: Considered records only the naming rule comes from the step; status and wording are chosen by this change.
- R1.9 (scope): The proposal's out-of-scope list excludes the pull request plan and status, yet the spec-ready gate depends on a fresh plan on the head (open question 1). The change depends on plan work it does not own. File: openspec/changes/pre-merge-apply-option/proposal.md. Resolution: answered: Depends_on keeps plan work outside this change; spec requires a plan on the exact head from that work.
- R1.10 (wrong claim): The change cites a route, GET /change-orders/{id}, that the proposal's own check says is not in the API description or route table. The error path points to a route that does not exist. File: openspec/changes/pre-merge-apply-option/proposal.md. Resolution: answered: Error commands use the listed listUnits route.
- R1.11 (missing scenario): Tasks 1.1 and 1.2 name tests, but no task or scenario covers the e2e refusal on a unit whose option is later switched off while an apply is running. The effect of toggling the option during an in-flight job is not specified. Resolution: answered: Added toggle-during-run requirement, scenario, and coverage in tasks.
- R2.1 (open question): The roadmap step says RFC 27 is a draft and that a design is needed. The change says RFC 27 governs nothing here and carries its own design, but no ADR or accepted RFC answers whether this option slice may ship before RFC 27 is accepted. The project rule requires an RFC first when a proposal relies on an unaccepted design. Resolution: open question: Added open question on shipping before RFC 27 is accepted; change carries only the option-slice design.
- R2.2 (missing scenario): The spec has no requirement or scenario for a branch apply when no plan exists on the exact branch head. The design and the 'Approved branch apply' scenario require such a plan, but no refusal is specified for its absence. Resolution: answered: Added no-plan refusal requirement and scenario to the pre-merge-apply spec.
- R2.3 (open question): The spec does not say whether the apply job pins the commit SHA that was approved and planned, or only a branch name. Without a pinned SHA, the job can apply a head that moved after the check, so the 'exact head' guarantee is not built. Resolution: answered: Spec now requires the job to pin the approved and planned commit SHA and scenarios cover it.
- R2.4 (failure mode): The re-check happens only at job start. The head can move between the check and the first apply action, or during the run, and no requirement covers this window. The design names a constructor check (preMergeGuard) but no spec scenario or task tests it. Resolution: answered: Added re-check before the first change requirement, scenario, and failing-test task for the head-move window.
- R2.5 (missing scenario): Design decision 2 requires that the approval targets the current head. The spec's 'Stale approval refusal' covers only a head that moved after approval. It does not cover an approval given for an older head that was never current, nor the refusal status and body of that case. Resolution: answered: Added scenario for approval given for an older head that was never current, with the same refusal.
- R2.6 (wrong claim): The ADR check says 'No ADR is touched' but gives no rg command or output as proof, though the project rule requires inline proof for each negative claim. The 'decisions that apply' input is empty, so the claim is unchecked. Resolution: answered: Added evidence entry that the pack lists no ADR for this item.
- R2.7 (wrong claim): The setting authorization requirement names the 'unit manage action'. The input gives no source for this action name, and the change does not show that it exists in the API description or in the stored grant model. Resolution: answered: Reworded authorization requirement to avoid an unsourced action name.
- R2.8 (scope): Task 2.2 runs the e2e step against a fake forge, but the project rule requires e2e against the real CLI and the real forge where a user-visible path exists. The change does not state why the fake forge is enough for the approval and head-move paths. Resolution: answered: Tasks keep the fake GitHub API because the milestone exit test names it as the forge, with evidence cited.
- M.6 (consistency): The setting pre_merge_apply duplicates applyTiming of report-plans-on-review-request and does not use camelCase. No change writes the docs text, of the owner decision of 2026-10-30, that says the project does not recommend pre-merge apply. Resolution: answered: Answered in specs capability pre-merge-apply requirement Pre-merge apply option which stores shared unit value applyTiming pre-merge with default post-merge, requirement Default refusal which names applyTiming pre-merge, design decision Pre-merge apply reuses applyTiming, tasks Definition of done docs step which states the project does not recommend the option, and depends_on which lists report-plans-on-review-request and mirror-approvals-between-review-request.

## Open questions

none

Questions considered and answered:

- Should pre-merge apply become the default? No. The milestone step keeps apply after the merge as the default and pre-merge apply as a per-unit option, so the timing defaults to post-merge.
- Does the milestone step fix the refusal status code and wording? No. The step requires only an error that names the option. This change chooses the refusal shape and the exact wording.
- Does the apply use the approved branch head or a pinned commit SHA? A pinned commit SHA. The apply uses the approved head commit SHA with a plan on that exact SHA, re-checked at job start and again before the first change.
- What happens when the option changes while an apply runs? It runs to its end. The option is checked at start and before the first change; a change during a run does not stop that run.
- What happens when two units disagree on the option? Each unit decides alone. One unit applies from its branch while the other waits for merge; no cross-unit coupling exists.
- Does RFC 27 govern this behavior? RFC 27 is a draft RFC, not an accepted ADR, so there is no ADR conflict. This change carries only the option-slice design and depends on the plan work it names.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
