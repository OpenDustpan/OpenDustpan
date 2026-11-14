Roadmap: `docs/roadmap.md:775` (A person confirms it).

## Why

**Persona: developer.** A developer asks Dusty to draft a change and then confirms it before anything applies, so work moves only after a person says yes and four-eyes still holds.

The user path this change must make work:

1. **Console.** Open the proposed change order from Dusty and read the plan revision.
2. **API.** Confirm the change order with a session credential; under a two-person rule ask a second person to approve.
3. **API.** Check jobs and audit log show no apply step before confirmation.
4. **Docs page.** Follow docs/guides/approval-rules.md to set authorMayApprove false.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Agent tries to confirm or approve its own change order. | 403 with the NEW error code `changeorder.agent-confirm-refused`; the response names the NEW person confirm route. | `POST /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/confirm (NEW)` |
| Delegator tries second approval under a two-person rule. | 403 with error code approval.not-eligible; the response names the approval rule and the authorMayApprove setting. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-rules` |
| Apply or destroy starts before a person confirms the revision. | 409 with the NEW error code `changeorder.not-ready`; the response names the NEW confirm route that must come first. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |
| Person confirms with an access token while a fresh step is needed. | 403 with error code mfa.fresh-step-required; the response names the enrollment page. | `POST /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/confirm (NEW)` |

Evidence for the claims above:

- Approval rules have e2e coverage in the pack. Proof: e2e/approval_rules_test.go and e2e/state_write_gate_test.go are listed for Approval rule.
- The audit log has e2e and guide coverage in the pack. Proof: e2e/audit_export_test.go and docs/guides/audit-log.md are listed for Audit log.
- No change-order e2e file is listed in the pack. Proof: The Change order entry lists e2e: [] and e2e_count: 0.

## What Changes

- Only a person principal confirms or approves a change order proposed by an agent; an agent request is refused.
- The delegator counts as author, so a two-person approval rule needs a different second approver.
- No apply job starts while the change order is in state proposed.
- The NEW audit export route GET /api/dustpan/v1/namespaces/{namespace}/audit shows the person confirmation entry before any apply entry of the revision.

Out of scope:

- Delegation record shape and revoke behavior
- Dusty run loop and agent tools
- Audit entry shape for agent steps
- Console screens for confirmation

Depends on:

- person-delegates-dusty
- dusty-proposes-change-order
- every-step-audited
- `person-delegates-dusty` (order of the milestone review)
- `dusty-proposes-change-order` (order of the milestone review)
- `every-step-audited` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The path still costs time when the person cannot see why a second approval is refused under authorMayApprove false.
- The delegator may expect one confirm to approve and apply; the console must show that confirmation and approval are separate steps.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a job gate status test for confirmation gates. (`pkg/job/gate_test.go:101 (TestConfirmationGateStatus)`)
- Main ships audit entries in order and moves cursors. (`pkg/audit/ship_test.go:257 (TestShipOnceCopiesEveryEntryInOrderAndMovesTheCursors)`)
- Main refuses a grant after a racing move. (`pkg/access/race_test.go:577 (TestGrantRefusedAfterARacingMove)`)
- Main leaves one entry and one operation for concurrent approvals. (`pkg/dustpan/auditchain_approval_test.go:108 (TestConcurrentApprovalsLeaveOneEntryAndOneOperation)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 190: Change orders: an object above jobs** (owner-tagged: decision 14).

- Decisions that apply: 14 (changed).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 299 changes decisions 4, 9, 10, 12 and 14; ADR 300 changes decision 10; ADR 344 changes decision 14; ADR 365 changes decision 10. Chain to the end: ADR 222, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 286, ADR 335, ADR 351, ADR 354, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365.
- Quote: "An agent records no verdict: it submits its blocking threads in a review of the verdict `comment` (ADR 192 decision 2)."
- Effect: The change keeps the agent limit: an agent leaves comments and blocking comments only and records no verdict, so confirmation and approval stay acts of a person.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 18 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "They keep their access tokens (ADR 53), with no change. An access token never gives a fresh authentication (ADR 227 decision 6)."
- Effect: Confirmation that needs a fresh step needs a session with that step; an access token alone never satisfies it.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No factor, no fresh step (owner). A person of these methods who has enrolled no factor cannot do a step that needs a fresh authentication."
- Effect: When confirm needs a fresh step and the person enrolled no factor, the server refuses with mfa.fresh-step-required.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The person who delegated may confirm (owner, 2026-11-07). Question 1. The person who delegated (Alice) may confirm a change that her own agent proposed, by default."
- Effect: The change follows the owner rule that the person who delegated may confirm the change of the agent; four-eyes comes only from the approval rule with authorMayApprove false.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- May the person who delegated confirm the change of the agent? Yes, by default. ADR 354 decision 1 allows it; a different second person is needed only when the namespace sets authorMayApprove false.
- May the agent confirm or approve its own change order? No. ADR 190 decision 14 records no verdict for an agent.
- Is a jobs-route check enough for no apply before confirmation? No. The roadmap step needs the audit log to show no apply step before confirmation, so the spec checks the audit log too.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
