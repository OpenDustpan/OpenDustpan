Issue: #890, #891, #894.

## Why

**Persona: developer.** Developer opens a pull request and wants the plan result of each stack and workspace without leaving the Git host. The roadmap fixes one commit status and one summary comment for each pull request in the first slice. Teams cannot pin one required check in branch protection, and no setting says when an apply runs, until this change ships.

The user path this change must make work:

1. **E2e.** Push to a pull request that touches three units. Read one summary check and one comment with a table row for each unit: status, counts, resource addresses with their kind of change, and a link to the change order.
2. **E2e.** Push again. Read the same comment with the new results. No second comment exists.
3. **API.** Set the apply timing of a namespace, a collection or a unit through the settings API. Read the effective value and the level that set it.
4. **Docs page.** Copy the fixed summary check name from the docs page into the branch protection of the Git host.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A unit write of pre-merge apply while a wider level forbids overrides | The server answers 409 with a message that names the level that forbids the override. Nothing changes. | `GET /api/dustpan/v1/namespaces/{namespace}/settings` |
| The Git host does not answer on a check or comment write | The write fails with the answer of the Git host. The summary check is not reported as passed. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- No code names applyTiming yet. Proof: rg -n -F --glob '!*_test.go' 'applyTiming' pkg internal cmd gives 0 matches.
- No code names allowOverride yet. Proof: rg -n -F --glob '!*_test.go' 'allowOverride' pkg internal cmd gives 0 matches.
- Issue 890 asks for one summary check and exactly one edited comment. Proof: Issue #890 acceptance criteria: a second push edits the same comment; counts match the plan of each unit.
- The settings API routes exist for the installation and for a namespace. Proof: Coverage gaps list GET /api/dustpan/v1/namespaces/{namespace}/settings (getNamespaceSettings) and GET /api/dustpan/v1/settings (getSettings).

## What Changes

- The Forge port gets a method to create or update one check and a method to edit one comment, each with conformance cases.
- A summary check name constant is documented for branch protection.
- A comment renderer builds one table with a row for each unit: status, counts, resource addresses with their kind of change, and a link to the change order. It shows no value.
- The setting applyTiming (post-merge or pre-merge) with allowOverride is new at namespace and collection level, with a unit value. It uses the generic settings API and writes audit entries.
- One lock for each review request serializes edits of the stored comment.

Out of scope:

- One check for each unit (a later change)
- Comment commands, including plan, apply and state commands (a later change)
- Redaction of a plan comment
- Approval mirroring between a review request and its change order
- GitLab, until its Forge adapter lands
- The docs text that explains why post-merge apply is recommended

Depends on:

- start-plan-on-push-review
- git-host-webhook-intake-post
- add-job-service-routes-start-1
- `start-plan-on-push-review` (order of the milestone review)
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The summary check name must be copied exactly from the docs page into branch protection.
- A 409 on the timing setting names the level that forbids the override.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged (all decisions)).

- Decisions that apply: 25 (current).
- Changed by: ADR 328 extends decision 1; ADR 359 extends decision 1; ADR 359 extends decision 15; ADR 359 extends decision 25. Chain to the end: ADR 328, ADR 359.
- Current rule: this ADR, with ADR 328, 359.
- Quote: "The plan comment (owner). One comment on the review request, edited in place, with a summary and a link. By default the summary holds counts and resource addresses with their kind of change, and never a value."
- Effect: One comment, edited in place, holds counts and resource addresses with their kind of change and no value.
- Conflict: none.

**ADR 359: Review requests: mirrored approvals, checks, comment commands and apply timing** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The table in the plan comment (owner, 2026-10-18). The server edits one comment in place (ADR 299 decision 25). It has a table with one row for each unit. A row shows the status, the `+ ~ -` counts and a link to the change order."
- Effect: The comment has one table row for each unit with status, counts and a link to the change order.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The docs page ships in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The summary check name and its states are open (proposal Open questions 1, owner). The spec-ready gate is not met, and the checks and comment cannot be built without the fixed name and fail rules. Resolution: open question: The check name is the one open question for the owner. The fail rules are fixed in the design and the spec.
- R1.2 (open question): Identity mapping and authorization actions for comment commands are open (proposal Open question 2, PM). ADR 299 decision 31 fixes the immutable user ID, and decision 20 fixes the credential rule, but the spec does not map them. The unknown-commenter and plan-start rules cannot be built. Resolution: answered: The credential rule and the immutable ID are in the command spec and the design.
- R1.3 (open question): The resolution order of applyTiming across namespace, collection and unit is open (proposal Open question 3, PM). The apply-timing spec gives only a two-level case and no collection level. Resolution: answered: The timing spec covers the collection level, and the design fixes the order from the widest scope.
- R1.4 (adr conflict): ADR 299 decision 25 says the plan comment holds a summary, and by default counts and resource addresses, never a value. The renderer spec adds no requirement for addresses, and the design says only counts, status and links. The change does not state whether addresses appear, so the comment rule is not fully met. Resolution: answered: The renderer rule includes addresses with their kind of change and no value.
- R1.5 (adr conflict): ADR 299 decision 20 gives one credential rule for every plan start: the opener and each pusher must be linked to a principal with unit:run-plan. The comment command spec only checks unit:run-plan for the commenter and ignores the opener and pushers, and it does not state the fork rule of decision 21. Resolution: answered: The plan command requires the opener, each pusher and the commenter to hold the plan action. The fork rule has its own requirement.
- R1.6 (missing scenario): The spec has no scenario for a comment from a fork or from a bot, nor for a comment that names a head commit that a new push has voided (ADR 299 decisions 21 to 23). Resolution: answered: Scenarios cover a fork comment and its void by a new push. A bot follows its stored service grant in the design.
- R1.7 (missing scenario): No requirement covers apply refusal under post-merge timing. The proposal error table promises a 403 naming the pre-merge option, but the command spec has no apply rule and no refusal reply, though the design lists it as a layer hostile test. Resolution: answered: A requirement and a scenario cover the apply refusal under post-merge timing.
- R1.8 (failure mode): Second push creates a second comment is named as a risk, but no requirement covers a failed edit, a deleted comment or a comment redacted under ADR 299 decision 26, where the server must delete and repost with a notice. Resolution: answered: The comment repair and redaction requirements cover a deleted comment and a redaction.
- R1.9 (failure mode): The design lists the risk that the summary check stays green while a unit waits on approval, but the spec covers only the fail case. It does not state whether a pending plan (not yet run) or a Git host outage fails the summary (ADR 299 decision 33). Resolution: answered: The summary rule fails for a plan not finished, and a Git host error follows its own answer.
- R1.10 (wrong claim): The proposal names a GET review-request route in the error table, and the code check fails: the route is not in the API description or the route table. The error table therefore points to a command that does not exist. Resolution: answered: The error table now uses routes of the pack only.
- R1.11 (scope): The tasks and proposal include comment commands, applyTiming and audit (#891, #894) in one change, but the issues list them as separate items with their own needs-adr status. The change also omits the GitLab item, which the issues defer. Its own scope in the proposal does not match its tasks on the audit of the commands. Resolution: answered: The scope is one slice with GitLab out of scope. The depends_on list now names an existing item.
- R2.1 (adr conflict): ADR 299 decision 25 (owner) says the summary holds counts and resource addresses with their kind of change, never a value, and the comment is edited in place. The renderer spec (specs/review-plan-report/spec.md, Comment table) and the design only give status, counts and links; the change claims addresses are covered but the spec scenario does not require them in the rendered output, so the owner-tagged decision is not met as written. Resolution: answered: The Comment table requirement and scenarios now require addresses with their kind of change and no value.
- R2.2 (adr conflict): ADR 299 decision 20 (owner) requires the opener and each pusher to be linked to a principal with unit:run-plan. The command spec (Plan command requirement) names the opener, pushers and commenter, but the proposal error table and the command service layer only check the commenter for the link; the bot case is only in the design, and the spec has no scenario for a missing opener link. Resolution: answered: The error table, the command layer and the spec cover the opener and each pusher, with an opener scenario.
- R2.3 (adr conflict): ADR 299 decision 26 (owner) says a redaction deletes the comment and posts a new one with a notice, and a redaction invalidates the plan. The spec for comment repair covers only a deleted comment (Comment repair requirement); the redaction path, the notice and the plan invalidation have no requirement or scenario. Resolution: answered: A Redaction requirement covers delete, notice and plan invalidation.
- R2.4 (adr conflict): ADR 299 decision 33 (owner): a Git host that does not answer fails the command with its own answer. The summary rule in specs/review-plan-report/spec.md covers failed, approval-wait and unfinished plans only; a Git host error or outage has no scenario, and the design layer table does not test it. Resolution: answered: A Host error requirement and scenario cover a Git host failure.
- R2.5 (adr conflict): ADR 299 decision 21 (owner): a fork review request gets no plan until a trusted person comments, and a new push voids the comment. The fork spec only says no plan starts; it has no requirement for the trusted-person definition (decision 40, owner) and no scenario for a fork comment that names the head commit. Resolution: answered: The fork requirement has a trusted comment scenario that binds to the head commit.
- R2.6 (open question): The apply-timing setting is new, but the spec gives no requirement for the case where a collection forbids overrides and the namespace allows them, nor for an unset level. Resolution order is fixed in the design only, and the apply-timing spec has no scenario for the collection-forbids case or the unit-without-value case. Resolution: answered: Scenarios cover a collection that forbids overrides and an unset unit.
- R2.7 (missing scenario): The apply-timing spec has no requirement for audit entries, though the proposal and the what_changes section say the setting writes audit entries (ADR 299 decision 44 and ADR 359 apply timing). Audit of the timing write is not specified. Resolution: answered: A Timing audit requirement covers the audit entry.
- R2.8 (missing scenario): The proposal error table promises a 403 naming the pre-merge option for apply under post-merge timing, but the apply-refused requirement gives a reply and no status code, and the 409 for the override refusal is not in any requirement. The API answer codes are not specified. Resolution: answered: The 409 for the override refusal is now in the requirement. The apply refusal is a reply, and the error table now says so.
- R2.9 (failure mode): Two pushes in quick succession, or a push during an edit, can race on the single comment. No requirement states how concurrent edits to the same stored comment ID are serialized, or what happens when the server crashes between posting and recording the comment ID (a second comment then appears). Resolution: answered: An Edit order requirement covers the lock and the crash between post and record.
- R2.10 (wrong claim): The proposal says the GitHub adapter is in scope, but the design and tasks say the Forge port gets methods with conformance cases while GitLab is out of scope; the proposal's Depends on list (start-plan-on-push-review, git-host-webhook-intake-post, add-job-service-routes-start) is not matched by any evidence in the input that these changes are merged, and the issue 890 Depends on list names #886 instead. Resolution: answered: The dependency list follows the milestone items. The issue names #886, and the matching items are the listed changes.
- R2.11 (open question): The provisional name opendustpan/plans is accepted, but issue 890 says the name needs a short ADR before code, and the proposal's own spec-ready gate lists the check name as open. The summary check name, which the owner must copy into branch protection, is not yet fixed by an ADR, so the gate is not met. Resolution: open question: The check name stays one open question for the owner. The recommended value is opendustpan/plans.
- M.13 (consistency): The unit checks go past the owner decision of one commit status for each pull request in the first slice. The change carries owner decision P5, which is not related to it, and it holds three issues. Resolution: answered: The change now has one summary check and one comment. Unit checks and comment commands are out of scope for a later change. The unrelated P5 line is removed from the considered list as a rule and kept only as a note that the default collection is a normal level. applyTiming stays here as the single timing setting.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the exact fixed name of the required summary check?
   - Options: opendustpan/plans; opendustpan/plan-summary.
   - Provisional decision (owner review 14 Nov): opendustpan/plans
   - Reason: Branch protection needs one stable name. No ADR fixes it yet. It is one constant and the docs page states it. Benchmark: Google: a documented, stable required check name that teams pin.
   - Persona impact: A Developer pins one name. A later rename breaks protection. Escape hatch: The docs page states the constant and the settings API shows the name in use.
   - Owner-tagged ADRs involved: ADR 299, ADR 359 (their words do not change).

Questions considered and answered:

- Do unit checks belong in this change? No. The roadmap fixes one commit status and one summary comment for each pull request in the first slice. Unit checks move to a later change.
- Do comment commands belong here? No. They need their own authorization actions and identity mapping. They move to a later change. This change keeps the single apply timing setting.
- Does one summary check plus one comment cover the report? Yes. ADR 359 decision 4 and ADR 299 decision 25 fix one edited comment with a table.
- Does the stored default collection of a new namespace change the timing levels? No. Owner decision, 13 Nov 2026 (queue P5): the default collection is a normal stored collection. It is one level for the setting, with no hidden rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
