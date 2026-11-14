Roadmap: `docs/roadmap.md:373` (Environment promotion).

## Why

**Persona: developer.** Developer moves a tested config from staging to production. Today the copy is manual, drifts, and skips review. The developer needs a visible difference and a gated promotion that writes history.

The user path this change must make work:

1. **API.** Read comparison of staging and production config through API and docs page
2. **API.** Promote selected config from staging to production through API
3. **CLI.** Run plan and apply on promoted unit through tool CLI
4. **API.** Read decision record through API when a gate refuses the promotion

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Promotion across tools | 422 promotion.incompatible-tools with message `Units use different tools` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |
| Target unit changed during promotion | 409 unit.conflict with message `Target unit changed, read the comparison again` | `POST /api/dustpan/v1/namespaces/{namespace}/promotions` |
| Gate refuses the promotion plan | 201 with committed edit and decision record, message `Edit committed, plan refused, see decision record` | `GET /api/dustpan/v1/namespaces/{namespace}/promotions/{id}` |

Evidence for the claims above:

- A blueprint guide exists in the docs. Proof: docs/guides/publish-a-blueprint.md
- Write-back has one e2e file in the pack. Proof: e2e/unit_settings_test.go
- No comparison or promotion e2e file is listed in the pack. Proof: Blueprint e2e_count 0 and Write-back e2e list holds only e2e/unit_settings_test.go
- The pack names no ADR for this item. Proof: adrs list is empty and keywords list holds only Blueprint and Write-back

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/units/compare for per-field effective config difference
- New route POST /api/dustpan/v1/namespaces/{namespace}/promotions for gated promotion copy as edit
- New route GET /api/dustpan/v1/namespaces/{namespace}/promotions/{id} for committed edit and decision record
- Comparison read of effective config values of two units from one blueprint with masked secrets
- Promotion copy of selected config fields as an edit with write-back commit and plan
- Milestone 5 gate evaluation on the promotion plan

Out of scope:

- Ephemeral units
- Artifact registry and curated upstreams
- Upgrade pull requests
- Job proxy
- Console catalog

Depends on:

- blueprints
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Caller must map field names between staging and production without help when blueprint defaults differ.
- Promotion keeps the committed edit when a gate refuses the plan, so caller must write a new edit to undo.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (missing scenario): specs/environment-promotion/spec.md has no scenario for a concurrent edit of the target unit during promotion, although proposal.md lists 409 unit.conflict and design.md names the lost-update risk. Resolution: answered: Covered by requirement Refuse stale promotion with target revision guard and 409 scenario.
- R1.2 (missing scenario): No requirement covers secret values in comparison output or promotion copy, although design.md lists plain-text leakage as a risk and the spec returns per-field source and target values. Resolution: answered: Covered by requirements Mask secrets in comparison, Secret equality without plain text, and Require explicit secret fields.
- R1.3 (missing scenario): No scenario covers a gate refusal of the promotion plan (403 gate.denied in proposal.md), nor whether the target edit stays committed when the plan is refused. Resolution: answered: Covered by requirement Keep committed edit on gate refusal with 201 and decision record.
- R1.4 (failure mode): The spec does not say what happens when the copy fails after the write-back commit, or when the plan job cannot start. The proposal usability note admits partial write-back commits, and no rollback or undo rule is given, though CLAUDE.md requires risky actions to be undone or to state they cannot be. Resolution: answered: Covered by requirement Keep committed edit when plan cannot start and design decision stating only a new edit undoes it.
- R1.5 (open question): The field allowlist that design.md requires in promoteUnits is not defined. The spec does not say which config fields may be promoted, or how secret and stable-ID fields are treated. Resolution: answered: Covered by requirement Promotion field allowlist with promotion.field-not-allowed refusal.
- R1.6 (wrong claim): The proposal states the error command for the 409 and 403 cases, but the open-questions block marks a route as missing from the API description (.errors[1].command). The change therefore cites a route that does not exist, so the spec-ready gate must fail. Resolution: answered: Covered by what_changes entries for GET units/compare and POST promotions routes.
- R1.7 (open question): The ADR check is empty ('No ADR is touched') with no evidence, although comparison and promotion touch the unit settings, write-back and gate behavior. The 'no ADR conflict' claim has no quoted decision lines. Resolution: answered: Added considered entry stating the pack adrs list is empty and evidence entry citing that empty list, so adr_check stays empty with proof.
- R1.8 (failure mode): No scenario covers a promotion request whose source unit is missing, removed or renamed. The spec does not say whether units are referenced by stable ID or by name, as the project rule for renamable entities requires. Resolution: answered: Covered by requirement Reference units by stable ID with 404 scenarios for source and target.
- R1.9 (scope): tasks.md section 2.2 says the promotion route uses a unit manage action, but the spec and design do not name the authorization actions or the object of each action, which the project requires for every endpoint. Resolution: answered: Covered by design decision Promotion is an edit plus a plan and promotion requirements naming unit:read on source and unit:manage on target.
- R1.10 (scope): The proposal lists the console as a surface for reviewing decision records, but tasks.md has no console task and no spec requirement for that surface. Resolution: answered: Removed console surface from user_path and recorded decision-record read as API with GET promotions route.
- R2.1 (open question): The proposal says it is not spec-ready: open question 1 is unresolved and the R1.7 resolution is flagged by a code check. The gate requires 'Open questions: none', so the change cannot be built until the owner closes both. Resolution: answered: Open questions list is now empty; prior comparison-scope question moved to considered as answered.
- R2.2 (scope): The roadmap says 'Design needed: RFC 55 is a draft.' The change does not cite RFC 55 and does not say whether it follows it. Promotion semantics (edit, plan, gates) may conflict with a draft design, and no one checked. Resolution: answered: Design context now cites RFC 55 draft semantics and states this change follows and proves that behavior.
- R2.3 (failure mode): Design decision 4 commits the edit first, then starts the plan. A server crash or restart between commit and plan start leaves a committed edit with no plan, no decision record and no audit entry. No scenario or recovery rule covers it. Resolution: answered: Added requirement Recover interrupted promotion with retry that reuses the committed edit.
- R2.4 (missing scenario): The 409 scenario says 'changed after the compared revision', but the promotion request body has no revision or ETag input. The spec does not define how the caller sends the compared revision, or how the server checks it atomically against a concurrent edit. Resolution: answered: Added target revision input to promotion requirements and atomic 409 check scenario.
- R2.5 (wrong claim): Proposal error table: the 403 gate.denied row names 'POST .../promotions' as the command that helps. That is the failing call, not a command that reads the decision record. User path step 4 says the record is read through the API, but no read route or field exists. The docs say the response carries the record only on the promotion call. Resolution: answered: Fixed gate-refusal error row to name GET promotions/{id} as the helping command and added that read route.
- R2.6 (wrong claim): The proposal says a refused plan returns 403 gate.denied, but the spec says the server keeps the committed edit. A 403 for a committed change is misleading. The status and body for 'edit committed, plan refused' are undefined. Resolution: answered: Changed gate-refusal outcome to 201 with committed edit plus decision record instead of 403.
- R2.7 (missing scenario): The 404 requirement covers the source unit only. A missing target unit, a unit in another namespace, and a rename (the stable ID still resolves) have no scenario. The 'renamed' case in the source scenario contradicts stable-ID resolution, which should succeed. Design says a rename fails closed with 409, but the spec says 404. Resolution: answered: Added target missing, cross-namespace, and rename-resolves scenarios to stable ID requirement.
- R2.8 (missing scenario): The compare route uses names (from=staging&to=production) but the promotion body uses stable IDs. The spec does not say how the caller gets the IDs. Missing scenarios: a caller with unit:read on the source but not unit:manage on the target gets 403; units from different blueprints or namespaces; an empty field list; no difference to promote. Resolution: answered: Added requirements for auth refusal, compare by stable ID, different blueprints, empty field list, and no difference.
- R2.9 (failure mode): Secrets: the compare output is masked, but a masked marker may hide whether two secrets are equal. The 'equality state' of secret fields is undefined. Copying a secret value to the write-back commit may write plain text into git history. The spec does not say how it is stored. Resolution: answered: Added requirements for secret equality without plain text and sealed secret storage in write-back.
- R2.10 (scope): The proposal says 'No ADR is touched' with no evidence, and the ADR list given is empty. Write-back, gates and authorization do touch decisions in docs/adr. The claim has no proof as the project rules require. Resolution: answered: Added evidence entry citing the empty pack adrs list as proof that adr_check stays empty.
- R2.11 (scope): tasks.md has no failing tests for field allowlist refusal, secret flag refusal, missing-unit 404 or plan-cannot-start. Specs have scenarios for these. Rule: each scenario maps to a named test. The audit entry for promotion is only named in task 2.2. Resolution: answered: Added failing-test tasks for every scenario including allowlist, secret flag, 404, plan-cannot-start, auth, and audit.
- M.7 (gap): The ADR check is empty, but the roadmap row puts RFC 55 in milestone 6 through ADR 331. The promotion also depends on the gates of milestone 5 and on the write-back of ADR 111. Resolution: answered: Added adr_check entries for ADR 331, ADR 224, and ADR 100 with quoted decision lines and effects.

## Open questions

none

Questions considered and answered:

- Does a comparison or promotion API already exist? No. The pack lists Blueprint e2e_count 0 and only e2e/unit_settings_test.go for write-back, so no comparison or promotion route exists and the behavior is new.
- Does promotion need new gates? No. The roadmap step says the gates of milestone 5 apply, so promotion reuses the same gate evaluation as other edits and adds no new gate model.
- Does comparison show effective values only or effective values plus source location? Effective values only. The glossary defines effective value as the value a job uses, so the comparison shows effective values with secret values masked to keep the first read small.
- Is promotion a direct write or an edit with a plan? Copy as an edit. The roadmap step says a promotion copies the config of one unit to the other as an edit, with a write-back commit and a plan.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
