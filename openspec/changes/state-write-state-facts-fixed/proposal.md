Issue: #1258.

## Why

**Persona: developer.** A developer pushes state with the Pulumi CLI or OpenTofu and the write stops at state.write, but the record shows state.available false, lists no fixed control and misses approval defaults, while access limits hold only at state.write routes, so the developer cannot tell what the gate saw or what to fix.

The user path this change must make work:

1. **CLI.** Run pulumi up or tofu apply against the server from the CLI.
2. **API.** Read the 403 refusal with the record ID on a gate or limit denial.
3. **Docs page.** Open the record and fix the state, lock, approval or limit, then retry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A gate refuses the state write | 403 {"code": "gate.denied", "message": "state.write refused by gate <id>, record <gdr-id>. Run dustpan gates show <gdr-id>."} | `dustpan gates show <gdr-id>` |
| An access limit refuses the state.write call on a unit, namespace, collection or installation object | 403 {"code": "gate.limited", "message": "access limit <id> refuses <action>, record <gdr-id>. Run dustpan gates show <gdr-id>."} | `dustpan gates show <gdr-id>` |

Evidence for the claims above:

- state.available defaults to false in the gate state-write path Proof: pkg/gate/statewrite.go:382
- state.resources and state.removed facts come from the state-change input Proof: pkg/gate/statechange.go:28
- CheckAccess evaluates access limits and LimitedAuthorizer wraps an inner authorizer Proof: pkg/gate/service.go:297

## What Changes

- Fill state.available, state.resources and state.removed for each tool-neutral operation that holds old and new state, with encrypted bodies mapped to available false and continued write.
- Write the allow record in the step transaction and the refusal record in its own transaction, and name the record ID in the audit entry.
- List workspace lock, manual lock, freeze, override switches and safe delete in the record.
- Apply approval defaults at state.write with distinct eligible approvers.
- Wire LimitedAuthorizer for the state.write routes and handle unit, namespace, collection and installation objects in CheckAccess.

Out of scope:

- New decision points
- Console run page
- Unit-import import object fact schema of ADR 241 decision 23
- Full access-limit cover on every non-state.write route, the own access-limit refusal record, and the break-glass skip, in add-access-limits-per-scope
- Distinct-approver combining across requirements, in several-approval-requirements-one-change

Depends on:

- add-access-limits-per-scope
- several-approval-requirements-one-change
- `add-access-limits-per-scope` (order of the milestone review)
- `several-approval-requirements-one-change` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refusal names the record ID and the one command that shows it, so the developer finds the gate without reading server logs.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 16).

- Decisions that apply: 16 (changed), 17 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:"
- Quote: "Break-glass accounts meet the gates. A session of a break-glass account (ADR 70) is outside the access limits (ADR 223 decision 7). Gates, approval rules and fixed controls hold for each change of such a session."
- Effect: Runs state.write checks before each write from a person's own CLI; fills state facts for the evaluated operation; a break-glass session stays outside access limits while gates hold.
- Conflict: none.

**ADR 229: Decision records, the simulate route and starter packs** (owner-tagged: decision 4).

- Decisions that apply: 4 (current), 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Fixed controls in the record (owner)."
- Quote: "The store checks the fence and writes it in the same transaction as the step that it explains."
- Quote: "A record of a refusal is written in its own transaction."
- Effect: Lists each fixed control in the record; writes the allow record in the step transaction and the refusal record in its own transaction.
- Conflict: none.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "An evaluation writes no audit entry of its own. The audit entry of the step that it explains names the record ID."
- Effect: Names the record ID in the audit entry of the step that the evaluation explains and writes no audit entry for the evaluation itself.
- Conflict: none.

**ADR 365: Wording refinements edit an ADR in place, with a changelog (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "With the owner's word ("A lgtm", 13 November 2026), decision 3 now says that such a state is not a read failure: `state.available` is `false`, the decision record says why, and the write goes on under the gates that apply."
- Effect: A client-encrypted or unreadable state gives state.available false with a reason and the write continues under the gates that apply.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): specs/state-write/spec.md: The spec and design do not follow the owner decision of 13 Nov (ADR 342 decision 3 as refined by ADR 365 decision 4): a client-encrypted or missing body must give state.available false with a recorded reason and the write continues under the gates. The design layer table says the fact builder rejects such bodies, which blocks the write instead. Resolution: answered: Facts decision and a client-encrypted requirement now state state.available false with reason and continued write; port adapter layer no longer rejects the body.
- R1.2 (adr conflict): design.md Decision 3 and the HTTP layer: 'evaluate access limits through gate.LimitedAuthorizer on every route' is said without a rule for the break-glass account. ADR 224 decision 17 says a break-glass session is outside access limits (ADR 223 decision 7); the spec does not state this exception. Resolution: answered: Design decision 3, limits requirement and break-glass requirement now state the ADR 224 decision 17 exception with its quote in adr_check.
- R1.3 (wrong claim): proposal.md ADR check: the quote from ADR 229 'The audit entry of the step that it explains names the record ID.' is not in the decision text as the input shows it. The Effect line claims it lists each fixed control, but ADR 229 decision 4 requires it in the record, and the quote cited is from ADR 230 decision 5, not 229. The open-questions gate is correctly marked not spec-ready. Resolution: answered: ADR check now quotes ADR 229 decisions 4 and 5 and ADR 230 decision 5 character for character from the pack.
- R1.4 (missing scenario): specs/state-write/spec.md 'Allowed write' and 'Locked workspace write' have no scenario for a refusal (deny, approval wait, rule failed). Record write must also run when the refusal is written in its own transaction (ADR 229 decision 5), and no requirement covers that. Resolution: answered: Added refusal-record requirement with deny and approval-wait scenarios for the own-transaction write.
- R1.5 (failure mode): design.md Decision 2 and the service layer: the step and the decision record share one transaction, but a fence loss or a store error between the guard and the commit is not covered. The record must not name a step that did not commit, and no scenario tests this rollback. Resolution: answered: Record decision and rollback requirement cover fence loss and store error with a rollback scenario.
- R1.6 (failure mode): specs/state-write/spec.md 'Approval defaults': 'waits for two distinct signatures' does not cover a signer who is the change author (ADR 224 decision 14 and the author-approval error gate.author-approval), a repeated signer, or an approver who lacks eligibility. No scenario covers these. Resolution: answered: Approval-defaults requirement now refuses author and repeated signatures and checks eligibility with new scenarios.
- R1.7 (missing scenario): specs/state-write/spec.md 'Limits on every route': only a namespace read is covered. No scenario covers unit, collection or installation objects, the 403 gate.limited body with the record ID, or an unauthorized caller reading a record (ADR 229 decision 6 skips unreadable records). Resolution: answered: Limits requirement adds unit, collection and installation scenarios with the 403 gate.limited body and the unreadable-record skip.
- R1.8 (open question): design.md Risks: 'cap counts' gives no number. ADR 230 decision 6 and the fact builder layer need a cap value and the behavior above it (state.available false or a refusal). Resolution: answered: Risk and fact builder state the 10000-entry cap and the truncation behavior above it.
- R1.9 (scope): proposal.md Out of scope lists 'New fact schema versions beyond state facts', but ADR 241 decision 23 adds an import object as a new minor fact schema version for state.write. The item must state whether unit-import is in or out. Resolution: answered: Out of scope now states the unit-import import object of ADR 241 decision 23 is out of this change.
- R1.10 (open question): tasks.md 2.4 and 1.1 to 1.5: no task covers the ADR 229 decision 6 simulate route or the e2e test of a locked tofu apply path through TFE-compatible unlocked-write (ADR 335 decision 3), which is a state.write route. Resolution: answered: Tasks add the simulate replay check and the TFE-compatible unlocked-write e2e step.
- R2.1 (owner decision conflict): design.md Layers, port adapter row: the fact builder is said to cap counted resources and keep raw states out of the record, but the change does not say what happens to the allow record when the body is client-encrypted; the owner decision of 13 Nov (ADR 365 decision 4) is met in the spec, yet the design hostile test still expects a capped count or a reason, which should be checked against the spec scenario for missing bodies. Resolution: answered: Facts decision and port-adapter layer now state encrypted bodies give state.available false with reason and no count, matching the client-encrypted scenario.
- R2.2 (failure mode): design.md Decision 2 and specs/state-write/spec.md 'Rollback record truth': the step and allow record share one transaction, but the refusal record is written in its own transaction, so a refusal that is recorded and then followed by a failed step leaves a record that names a refused step; no scenario covers this ordering. Resolution: answered: Record decision and refusal requirement now state a refusal record is a refused attempt and add a scenario that it never claims a step committed.
- R2.3 (missing scenario): specs/state-write/spec.md 'Approval defaults': no scenario covers an approver who lacks eligibility, nor a state.write approval wait that expires or is withdrawn while the store is in a fence loss; the eligibility requirement stated in the proposal is not tested. Resolution: answered: Approval-defaults requirement adds ineligible-signer and fence-loss-during-wait scenarios.
- R2.4 (failure mode): design.md Risks: the 10000-entry cap sets state.available true for the counted prefix, but a concurrent write during fact build can change the state between the guard read and the commit; no requirement says the fact build reads a snapshot that matches the committed step. Resolution: answered: Facts decision and risks now require facts from the operation states with fence rollback, and the rollback scenario covers a concurrent write.
- R2.5 (open question): specs/state-write/spec.md 'Break-glass outside limits': the break-glass session is said to skip access limits, but no scenario covers an agent session or a delegated request acting under a break-glass account (ADR 354), which is not stated. Resolution: answered: Break-glass requirement adds a delegated-agent scenario that follows the same skip-limits rule with gates held.
- R2.6 (missing scenario): specs/state-write/spec.md 'Limits on every route': no scenario covers an unauthorized caller reading a record of a refused call, beyond the skip rule in the proposal; the 403 body for a caller without read access to the record is not specified. Resolution: answered: Limits requirement adds a scenario for a caller without record read access that still gets the 403 with the record ID and no record content.
- R2.7 (wrong claim): proposal.md ADR check, ADR 224 entry: the 'Effect' says the change fills state.resources and state.removed, but the quoted decision 16 line is about the persona's own CLI and the ADR check lists it as a change; the quote does not show that tool-neutral operations are covered. Resolution: answered: ADR 224 effect now describes the quoted Version 1 check without claiming the quote covers tool-neutral operations; the fill behavior is stated as the change on top of it.
- R2.8 (scope): tasks.md 2.5 and proposal.md 'Out of scope': the docs page work and the simulate replay of ADR 229 decision 6 are in tasks, but the proposal says the simulate route is existing behavior needing no new route; the task still asks to check replay of stored state.write records, which is not a stated requirement. Resolution: answered: Added a simulate-replay requirement so the replay task maps to a stated behavior.
- R2.9 (open question): design.md Decision 3: an access limit on a unit object runs through LimitedAuthorizer, but the change does not say which record is written when CheckAccess refuses before any state.write evaluation exists (no step to explain). Resolution: answered: Design decision 3 and a new access-limit-record requirement state the own access-limit record for a CheckAccess refusal with no state.write step.
- M.33 (duplicate): 'Access-limit refusal record' and 'Break-glass outside limits' duplicate add-access-limits-per-scope. Resolution: answered: Removed the duplicate access-limit refusal record and break-glass requirements; this change now wires LimitedAuthorizer only at the state.write routes and depends on add-access-limits-per-scope and several-approval-requirements-one-change for the rest.

## Open questions

none

Questions considered and answered:

- Must state.resources and state.removed have values when the operation holds both states, including encrypted bodies? Yes. ADR 224 decision 16 covers writes from a person's own CLI, and ADR 365 decision 4 says a client-encrypted or missing body gives state.available false with a reason while the write continues.
- Must allow and refusal records link from the audit entry with fixed controls listed? Yes. ADR 229 decisions 4 and 5 require fixed controls in the record, the allow record in the step transaction, the refusal record in its own transaction, and ADR 230 decision 5 requires the record ID in the audit entry of the step.
- Must access limits hold on every route and what is the break-glass exception? This change wires the limited authorizer only at the state.write routes. Full access-limit cover on every route, the own access-limit refusal record, and the break-glass skip belong to add-access-limits-per-scope; distinct-approver combining belongs to several-approval-requirements-one-change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
