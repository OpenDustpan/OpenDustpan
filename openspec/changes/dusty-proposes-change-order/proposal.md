Roadmap: `docs/roadmap.md:772` (Dusty proposes a change order).

## Why

**Persona: developer.** Developer asks Dusty to draft infrastructure work. Developer needs a proposed change order that names them as proposer and names Dusty and the delegation, so review and confirm stay with people.

The user path this change must make work:

1. **Console.** Person asks Dusty to propose a change for one unit, under a live delegation.
2. **API.** Dusty writes a draft through the existing draft store, with the person, the agent, the delegation and a run, turn and call index key.
3. **Console.** Person confirms the draft. The server creates the change order in state proposed.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Delegation expired or revoked at draft | The server denies the draft with the NEW error code `delegation.expired` and stores nothing. | `make e2e` |
| Person lacks the right to propose on the unit | The server denies the draft with the NEW error code `changeorder.denied` and stores nothing. | `make e2e` |

Evidence for the claims above:

- No e2e test names Dusty in this pack Proof: evidence.Dusty.e2e is empty with e2e_count 0
- No e2e test names Change order in this pack Proof: evidence.Change order.e2e is empty with e2e_count 0
- A delegation grant pin rule exists on main Proof: TestGrantOnAPrincipalTakesDelegationActionsOnly at pkg/access/delegation_pin_test.go:58

## What Changes

- Dusty writes a draft through the existing draft store, with the person, the agent dusty and the delegation as actor.
- The person confirm-draft creates the change order in state proposed, with the person as proposer, naming the agent and the delegation.
- The server re-checks the live delegation and the person right at the confirm-draft.
- Draft and confirm-draft audit entries go through every-step-audited.

Out of scope:

- The audit of every step, written through every-step-audited
- A revoke that stops running work after the confirm-draft
- The MCP endpoint and the stdio proxy
- The agent verdict rule, held by changeorder.CheckAgent
- The creation of the delegation record
- The confirm of a proposed change order, held by person-confirms

Depends on:

- person-delegates-dusty
- every-step-audited
- `person-delegates-dusty` (order of the milestone review)
- `every-step-audited` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The proposer is the person, and the agent and the delegation are named.
- Confirm-draft and the later confirm of the proposed order use different names, routes and actions, so the person sees which step they do.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The delegation grant pin test exists on main (`pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decisions that apply: 11 (current), 13 (changed).
- Changed by: ADR 128 changes decision 22; ADR 129 changes decisions 3, 7 and 20; ADR 129 extends; ADR 210 changes; ADR 297 changes decisions 13 and 14; ADR 311 extends; ADR 314 extends; ADR 354 changes decisions 13, 14, 15 and 16. Chain to the end: ADR 128, ADR 129, ADR 210, ADR 297, ADR 311, ADR 314, ADR 354, ADR 190, ADR 222, ADR 227, ADR 286, ADR 247, ADR 301, ADR 333, ADR 357, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354.
- Quote: "Effects happen once. A step that proposes or cancels a change passes a key that it makes from the run, the turn and the index of the call."
- Effect: The draft write uses a key from run, turn and call index. The run acts for the delegating person.
- Conflict: none.

**ADR 190: Change orders: an object above jobs** (owner-tagged: decision 4, 14).

- Decisions that apply: 2 (current), 4 (changed), 14 (changed).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 299 changes decisions 4, 9, 10, 12 and 14; ADR 300 changes decision 10; ADR 344 changes decision 14; ADR 365 changes decision 10. Chain to the end: ADR 222, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 286, ADR 335, ADR 351, ADR 354, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365.
- Quote: "The authors. The authors of a change order are the principal that created it and each principal that attached a plan revision to it (ADR 191 decision 2)."
- Quote: "Agents. An agent reads a change order and leaves comments, when a later ADR gives it a tool."
- Effect: The proposer is the person who confirms the draft. The agent writes a draft only and records no approval.
- Conflict: none.

**ADR 196: Change order ports, errors, fences and conformance suites** (not owner-tagged).

- Decisions that apply: 8 (current), 10 (current).
- Changed by: ADR 222 changes decisions 1 and 5; ADR 230 changes decisions 1 and 5. Chain to the end: ADR 222, ADR 230, ADR 286, ADR 335, ADR 351, ADR 354, ADR 268, ADR 355.
- Current rule: this ADR, with ADR 222, 230.
- Quote: "The service. `changeorder.Service` is what the REST API calls. It takes an actor with the principal, the agent reference and the credential (ADR 128 decisions 1 and 6), checks the actions of ADR 197, writes the audit entries, and calls the store."
- Effect: The service takes the agent reference. The draft rule stays in CheckAgent.
- Conflict: none.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged (all decisions)).

- Decisions that apply: 35 (current).
- Changed by: ADR 328 extends decision 1; ADR 359 extends decision 1; ADR 359 extends decision 15; ADR 359 extends decision 25. Chain to the end: ADR 328, ADR 359.
- Current rule: this ADR, with ADR 328, 359.
- Quote: "Agents never approve, on either side (owner)."
- Effect: The draft and the confirm-draft record no approval.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which authorization action and object guard the draft and the confirm-draft, and what is the credential shape?
   - Options: Confirm-draft uses the existing create check on the unit; the draft carries the delegation ID with the access token of the person; New actions changeorder:draft and changeorder:confirm-draft, with a short-lived bound token.
   - Provisional decision (owner review 14 Nov): Confirm-draft uses the existing create check on the unit; the draft carries the delegation ID with the access token of the person
   - Reason: The pack gives no text of the action rules or the credential shape. Benchmark: Google Cloud IAM names one permission per method and denies by default.
   - Persona impact: Developer sees one denied message that names the fix. Escape hatch: A denied call stores nothing, so a person retries after the grant.
   - Owner-tagged ADRs involved: ADR 190 (their words do not change).

Questions considered and answered:

- May Dusty approve? No. ADR 299 decision 35 forbids it.
- What do ADR 344 and ADR 354 change here? The pack gives no decision text for them, so they are not quoted. They change the agent rule of ADR 190 decision 14 to a draft that a person confirms. This change follows that effect and asks the shape of the credential as an open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
