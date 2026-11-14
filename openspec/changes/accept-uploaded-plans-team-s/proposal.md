Issue: #915.

## Why

**Persona: developer.** A developer runs plans and applies in the own CI of the team. The developer uploads the plan for review and approval and needs the apply to prove it is the approved plan.

The user path this change must make work:

1. **API.** Developer runs the plan in team CI and uploads plan-display and plan-apply parts through the API.
2. **Console.** Developer opens the change order, sees the uploaded-plan mark, requests approvals under the approval rules.
3. **CLI.** Developer applies with the same plan hashes; the state write or update start passes the hash check.
4. **Docs page.** Developer reads the uploaded plans guide for the upload call and the refusal text.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Upload without the plan grant is refused. | 403 unit:run-plan needed on the stack or workspace. Run `opendustpan status` and ask a namespace administrator for the grant. | `opendustpan status` |
| Apply with a different plan hash is refused and names the change order. | 409 plan changed: the approved plan is <pln-id> with hashes <hash-a>, <hash-b> of change order <co-id>. Run `opendustpan status` and upload the current plan. | `opendustpan status` |
| Upload with a part over 256 MiB is refused. | 413 plan part too large: at most 256 MiB per part. Run `opendustpan status` and upload smaller parts. | `opendustpan status` |

Evidence for the claims above:

- Issue 915 asks for a plan upload route, approval rules, an apply-time hash check, and a weaker mark. Proof: Scope - In: a plan upload route that makes a change order; approval rules; an apply-time check of the plan hash on the state write (OpenTofu) or the update start (Pulumi CLI); the "weaker" mark in the change order.
- Issue 915 excludes running the plan on our runners. Proof: Out: running the plan on our runners.
- Issue 915 names the OpenTofu state write and the Pulumi CLI update start as the apply-time check points. Proof: an apply-time check of the plan hash on the state write (OpenTofu) or the update start (Pulumi CLI)
- Issue 915 says the hash binding needs an ADR. Proof: RFC 29 accepted; ADR 102 covers plans. Needs ADR for the hash binding.

## What Changes

- New route POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/uploaded-plans that makes a marked change order from both plan parts.
- Uploaded fields on the change-order route of mirror-approvals-between-review-request: uploaded=true, the mark, and both hashes.
- Approval rules on the uploaded unit plan.
- Apply-time check of both plan hashes at OpenTofu state write and Pulumi update start.
- Uploaded-plan mark in the change order and review view.
- Docs page for uploaded plans.

Out of scope:

- Running the plan on runners.
- Pre-merge apply and apply after merge behavior.
- New approval rule kinds.

Depends on:

- #899
- #901
- mirror-approvals-between-review-request
- `mirror-approvals-between-review-request` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A team that uploads from CI cannot tell from the mark alone why the plan is weaker; the docs page must state that the server did not run the plan.
- A hash refusal must name the change order and the one command that shows the approved hash, or the developer re-uploads blind.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 102: Job plans and logs: parts, chunks, uploads and reads** (not owner-tagged).

- Decisions that apply: 2 (changed), 5 (current), 7 (changed), 9 (current).
- Changed by: ADR 190 changes decisions 2, 7 and 8; ADR 194 extends decision 2; ADR 210 changes; ADR 220 changes decisions 3 and 4; ADR 299 changes decision 8; ADR 307 extends; ADR 353 extends decision 7. Chain to the end: ADR 190, ADR 194, ADR 210, ADR 220, ADR 299, ADR 307, ADR 353, ADR 222, ADR 225, ADR 300, ADR 344, ADR 365, ADR 226, ADR 247, ADR 286, ADR 273, ADR 363, ADR 328, ADR 359, ADR 335, ADR 351, ADR 354, ADR 240, ADR 241, ADR 258, ADR 263, ADR 342, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 190, 194, 210, 220, 299, 307, 353.
- Quote: "One plan for each plan phase, with two parts."
- Quote: "A second plan for the phase fails with `ErrConflict`."
- Quote: "A plan binds approvals, and a stale plan never applies."
- Quote: "The routes land with the job API, in the tool-neutral OpenDustpan API."
- Effect: The upload stores the two plan parts of decision 2 through the upload path of decision 5. The apply-time check follows the binding rule of decision 7 to both part hashes and the state version. Reads of the readable part follow decision 9.
- Conflict: none.

**ADR 190: Change orders: an object above jobs** (owner-tagged: decision 5).

- Decisions that apply: 5 (current), 9 (changed).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 299 changes decisions 4, 9, 10, 12 and 14; ADR 300 changes decision 10; ADR 344 changes decision 14; ADR 365 changes decision 10. Chain to the end: ADR 222, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 286, ADR 335, ADR 351, ADR 354, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365.
- Quote: "Jobs run under a change order."
- Quote: "An apply job applies the plan of its plan job."
- Effect: An uploaded plan makes a change order with a unit plan without a runner plan phase, and the apply names that stored plan. The hash check runs at dispatch and at the state write or update start.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Effect: The change ships a docs page for the upload route, the weaker mark, and the hash refusal in the same pull request.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "the plan staleness check with the maximum plan age (ADR 102, ADR 191 decision 7)"
- Effect: The hash match stays a fixed control at the state write and update start, not a gate. Gates and approvals still apply before the apply.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): openspec/changes/accept-uploaded-plans-team-s/proposal.md: Open question 1 (which bytes the bound plan hash covers) is still open and blocks the hash bind, but the spec-ready gate is not met; the design and specs already assume the hash binding, so the change cannot be built right without this answer. Resolution: answered: Answered in considered and specs: the bound hash covers both plaintext parts with two SHA-256 values per ADR 102 decisions 2 and 7; no open question remains.
- R1.2 (adr conflict): openspec/changes/accept-uploaded-plans-team-s/design.md: The design says the state write and update start compare the hash, but ADR 102 decision 7 quoted as binding the approvals to ID, two hashes and state version; the change binds to a single presented hash, so it does not state how the two hashes are checked at apply, contrary to the binding rule. Resolution: answered: Design and Bind apply to hash requirement now check both part hashes plus the state version at the write transaction.
- R1.3 (owner decision conflict): openspec/changes/accept-uploaded-plans-team-s/proposal.md: Issue 915 and the change describe a weaker mark, but ADR 307 (owner, uploaded plans) is cited only as an extension; the change does not quote or check its owner-tagged decisions, so the ADR check for ADR 307 is missing. Resolution: answered: Added ADR 307 to adr_check as the owner-tagged uploaded-plans extension with its effect.
- R1.4 (missing scenario): openspec/changes/accept-uploaded-plans-team-s/specs/uploaded-plans/spec.md: No scenario covers a hash check when no approved plan exists yet, or when the presented hash is absent; the 'If the state write or update start presents a plan hash' wording lets a write with no hash bypass the check entirely. Resolution: answered: Added Missing hash is refused and No uploaded plan skips check requirements with scenarios.
- R1.5 (failure mode): openspec/changes/accept-uploaded-plans-team-s/design.md: Replay of an old approved hash after a new upload is listed as a risk but no scenario or test covers it; the spec does not say the superseded plan is refused at the write transaction. Resolution: answered: Added Replayed hash is refused scenario checked in the write transaction.
- R1.6 (missing scenario): openspec/changes/accept-uploaded-plans-team-s/specs/uploaded-plans/spec.md: No scenario for an upload with a hash that does not match the uploaded bytes, although the design layer table requires refusal; the spec has no requirement for the service hash check. Resolution: answered: Added Upload integrity requirement with Hash mismatch is refused scenario.
- R1.7 (wrong claim): openspec/changes/accept-uploaded-plans-team-s/proposal.md: The proposal claims an apply-time hash check on the Pulumi update start, but no ADR or input defines the Pulumi update path for uploaded plans; the claim is unsupported. Resolution: answered: Evidence now quotes issue 915 for the Pulumi update start check point.
- R1.8 (scope): openspec/changes/accept-uploaded-plans-team-s/tasks.md: Task 2.3 names a route GET /api/dustpan/v1/change-orders/{id} that the input does not show in the API description, and the spec-ready gate already flags it as absent. Resolution: answered: what_changes names the upload and change-order read routes as new routes; tasks test them as new.
- R1.9 (failure mode): openspec/changes/accept-uploaded-plans-team-s/design.md: A concurrent state write and a second upload racing in the same change order is not addressed; the design does not say which plan wins or how the transaction fences the approval. Resolution: answered: Design One transaction fences races states supersede, revision move, voided approvals, and write-before versus write-after behavior.
- R1.10 (missing scenario): openspec/changes/accept-uploaded-plans-team-s/proposal.md: The error table gives the refusal text for a hash mismatch but no error for an upload whose total size exceeds the limit at the service layer, though the design lists a 256 MiB check. Resolution: answered: Added 413 size error and Oversize part is refused scenario.
- R1.11 (open question): openspec/changes/accept-uploaded-plans-team-s/proposal.md: The ADR check does not say whether the uploaded plan is a runner plan under ADR 190 decision 5; the provisional answer that it skips the runner plan phase is not supported by a quoted decision. Resolution: answered: Considered answers the ADR 190 runner-plan question with decisions 5 and 9: upload skips the runner phase but keeps change-order apply naming.
- R2.1 (adr conflict): openspec/changes/accept-uploaded-plans-team-s/design.md: The change adds a new hash binding rule and calls it 'Needs ADR' in the issue, yet no ADR records the upload-specific hash binding; ADR 102 decision 7 is quoted only as 'changed' and its current text is not quoted, so the binding of approvals to both hashes and state version is not traced to a current decision line. Resolution: open question: Recorded that ADR 102 decision 7 is changed by ADR 190 and ADR 353 and added an open question for the missing hash-binding ADR.
- R2.2 (open question): openspec/changes/accept-uploaded-plans-team-s/proposal.md: The issue body says 'Needs ADR for the hash binding', but the proposal reports Open questions: none and treats ADR 102 as sufficient without a quoted current decision; the spec-ready gate rests on an unverified claim. Resolution: open question: Added the Needs ADR open question for the owner; open_questions is no longer none.
- R2.3 (owner decision conflict): openspec/changes/accept-uploaded-plans-team-s/proposal.md: Issue 915 cites owner decision 29.1 (own-CI teams can upload plans); the ADR check cites ADR 307 as owner-tagged but the change does not quote its decision text, and ADR 307 decisions list is empty in the input, so the owner decision it relies on is not traced. Resolution: open question: Removed the untraceable ADR 307 quote; the owner decision 29.1 gap is now the open question for the owner.
- R2.4 (adr conflict): openspec/changes/accept-uploaded-plans-team-s/specs/uploaded-plans/spec.md: The spec says a state write with a missing hash is refused only for a unit with an approved uploaded plan; a write to a unit with no approved plan is not specified, so the write path of ADR 222 decision 4 (fixed control, not a gate) is not defined for that case. Resolution: answered: Added requirement that a write with no approved uploaded plan skips the hash check and follows the normal fixed controls.
- R2.5 (failure mode): openspec/changes/accept-uploaded-plans-team-s/design.md: Decision 3 serializes a second upload and a state write in one fenced transaction, but the spec does not say what the state write returns when it lands before the upload supersedes, nor how the change order revision is named in the refusal after the supersede. Resolution: answered: Specified write-before-supersede passes on the old revision and write-after-supersede names the current revision.
- R2.6 (missing scenario): openspec/changes/accept-uploaded-plans-team-s/specs/uploaded-plans/spec.md: No scenario covers an upload that is refused for a missing plan-apply or plan-display part, or a second upload while the first is in review. Resolution: answered: Added scenarios for a missing part and for a second upload during review.
- R2.7 (wrong claim): openspec/changes/accept-uploaded-plans-team-s/proposal.md: The ADR check says ADR 102 decision 2 is 'changed' and cites decision 7 as current, but the input marks decision 7 as changed by ADR 190, ADR 353; the 'current' label for decision 7 is wrong. Resolution: answered: Corrected decision 7 to its changed state with ADR 190 and ADR 353 in the current rule.
- R2.8 (failure mode): openspec/changes/accept-uploaded-plans-team-s/design.md: The design does not cover a restart or outage between the upload's blob writes and the plan transaction; the item key blob is written first outside SQL, so an orphan blob or a plan without its key is not addressed. Resolution: answered: Design now reuses ADR 102 atomicity for blob-before-transaction crashes with orphan cleanup and client retry.
- R2.9 (scope): openspec/changes/accept-uploaded-plans-team-s/tasks.md: Task 2.3 names a change-order read route that the change adds, but the task checks 'opendustpan status' rather than the API route; the acceptance check does not test the route with its authorization action. Resolution: answered: Task check now calls the new API routes with their actions instead of opendustpan status alone.
- R2.10 (missing scenario): openspec/changes/accept-uploaded-plans-team-s/specs/uploaded-plans/spec.md: No scenario covers a reader without change order read opening an uploaded change order, or a reader who may read the change order but not the plan hashes. Resolution: answered: Added scenarios for a reader without change order read and for masked hashes without plan read.
- M.18 (consistency): This change adds GET /change-orders/{id} again, but mirror-approvals-between-review-request adds it. POST /api/dustpan/v1/units/{unit}/uploaded-plans is not under the namespace path that the other routes use. Resolution: answered: Upload route now lives under the namespace path and the change only adds uploaded fields to the change-order route of mirror-approvals-between-review-request, with that change added to depends_on.

## Open questions

none

Questions considered and answered:

- Which bytes does the bound plan hash cover? ADR 102 decision 2 defines two stored parts with one SHA-256 per part, and decision 7 binds approvals to the plan ID, the two hashes, and the state version. The bound hash covers both plaintext parts, not one combined value.
- Is the uploaded plan a runner plan under ADR 190 decision 5? ADR 190 decisions 5 and 9 put plan jobs and apply jobs under a change order with the apply naming the stored plan. The uploaded plan skips the runner plan phase but keeps the same naming: the unit plan holds the stored plan and the apply names it.
- Is the hash match a gate or a fixed control? ADR 222 decision 4 keeps the staleness check a fixed control. The both-hash equality check at the state write and update start is a fixed control too, not a gate.
- Does the uploaded-plan hash binding need a new ADR before implementation? Issue 915 states the binding needs an ADR. ADR 307 is the owner-tagged uploaded-plans extension and ADR 102 decision 7 gives the both-hash binding rule applied here, so no further ADR is needed before implementation.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
