Roadmap: `docs/roadmap.md:772` (Dusty proposes a change order).

## Why

**Persona: developer.** A developer asks Dusty to draft infrastructure work. The developer needs a draft that names them, Dusty and the delegation, so that the review and the confirm stay with people.

The user path this change must make work:

1. **Console.** The person asks Dusty to propose a change for one unit, under a live delegation.
2. **API.** Dusty writes a draft, with the person, the agent, the delegation and a key from the run, the turn and the call index.
3. **Console.** The person confirms the draft. Only then does a change order exist (ADR 344 decision 2).

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The delegation ended before the draft | 403 with the NEW error code `delegation.ended` of `revoke-stops-work`. Nothing is stored. | `GET /api/dustpan/v1/delegations` (from `person-delegates-dusty`) |
| The delegation does not cover the unit | Refused. The code is open question 4. Nothing is stored. | `GET /api/dustpan/v1/delegations` |

Evidence for the claims above:

- No change order package exists on main. Proof: `ls pkg/changeorder` fails, and `rg -n 'changeorder' -g '*.go' pkg internal` finds no package of that name. The earlier draft of this change named an existing draft store, `changeorder.Service` and `CheckAgent`. None exists on main.
- No draft state exists in the code or in an ADR. Proof: ADR 344 line 27: "This ADR does not decide the state of a draft, the tool in ADR 122, the action name or the audit entry. A later ADR decides them."
- A grant on a principal takes act-as actions only. Proof: `pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly)`.

## What Changes

- Dusty writes a draft of a change order for a unit, under a live delegation of the mode `propose` or `operate` (RFC 121 decision 12). The draft names the person, the agent and the delegation, and carries the key of ADR 120 decision 11.
- The server resolves the delegation at the draft (RFC 121 decisions 9 and 31) and refuses an ended delegation.
- A draft is not a change order. The person confirms it in `person-confirms` (ADR 344 decision 2).
- The draft write goes through the audit entry of `every-step-audited`.

Out of scope:

- The confirm of the draft, in `person-confirms`.
- The audit entry shape, in `every-step-audited`.
- The refusal after an end, in `revoke-stops-work`.
- The delegation record, in `person-delegates-dusty`.
- The change order package and its routes, in the change order work. They are not on main.
- The tool of ADR 122 and the MCP endpoint.

Depends on:

- `person-delegates-dusty` and `every-step-audited`.
- A change that makes the change order package (ADR 190, ADR 196). None is in `openspec/changes/` on 20 November 2026. It is a dependency to be written.
- A model-connect change that makes Dusty. It is a dependency to be written.

## Usability

Where the path still costs the person time or surprises them:

- The person sees the agent, the delegation and the person on the draft, so that they know whose draft they confirm.
- A draft and the later change order use different words, so that the person knows which step they do.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A grant on a principal takes act-as actions only. (`pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`.

**ADR 344: An agent drafts a change order, and a person confirms it** (owner-tagged).

- Decisions that apply: 1 and 2 (current). Changed by: -. Chain to the end: none. Current rule: this ADR. It changes ADR 190 decision 14.
- Quote, decision 2: "The draft is not a change order until a person confirms it."
- Quote: "This ADR does not decide the state of a draft, the tool in ADR 122, the action name or the audit entry. A later ADR decides them."
- Effect: the draft state, the action name and the audit entry are open questions here, not decisions.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged) with **RFC 121** (accepted by ADR 354 decision 6).

- Decision 1 (current): "The person who delegated may confirm (owner, 2026-11-07)."
- RFC 121 decision 24: a change order that an agent drafts waits for the ADR 344 confirmation. Decision 26: the person is the proposer, and four-eyes sees the person.
- Effect: the proposer is the person. The agent and the delegation are named on the draft.
- Conflict: none.

**ADR 190: Change orders: an object above jobs** (decisions 4 and 14 are owner-tagged).

- Decision that applies: 14 (changed by ADR 344). Changed by: ADR 222, 225, 299, 300, 344, 365. Chain to the end: ADR 222, 225, 299, 300, 344, 365, 286, 335, 351, 354, 328, 359, 377. Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365.
- Effect: an agent may draft and a person confirms.
- Conflict: none.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged). Decision 11 is current. Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354, 367.

- Quote, decision 11: "Effects happen once. A step that proposes or cancels a change passes a key that it makes from the run, the turn and the index of the call."
- Effect: the draft write carries that key.
- Conflict: none.

**ADR 299: one integration with the Git host** (owner-tagged). Current rule: this ADR, with ADR 328, 359, 368.

- Quote, decision 35: "Agents never approve, on either side (owner)."
- Effect: a draft records no approval.
- Conflict: none.

## Open questions

Open questions: 5, parked

The questions below are not decided. No ADR line and no RFC line settles them. The owner decides each one.

1. **Where does a draft live, and what is its state?** ADR 344 does not decide the state of a draft. The roadmap says "proposed". ADR 344 says that the draft is not a change order until a person confirms. Options:
   - (a) A separate draft record in its own table. Cost: a second object that a person lists and cleans up.
   - (b) A change order in a state `draft` that no review rule sees. Cost: ADR 344 says it is not a change order, and a state in the table says it is.
   - Decides: the owner.
2. **What are the action names and the routes of the draft?** ADR 344 leaves the action name open. The earlier draft invented `changeorder:confirm-draft` and a route. Options: (a) one action for the draft write; (b) an action for each mode. Cost of (b): more rows in the mode table of `person-delegates-dusty` open question 8. Decides: the owner.
3. **What happens to a draft when the delegation ends before the confirm?** RFC 121 decision 23 covers a held request and not a draft. Options: (a) the draft stays and the person may confirm it (the person is the proposer, RFC 121 decision 26); (b) the confirm refuses it with `delegation.ended`; (c) the draft is canceled with the stop reason `delegation-ended`. Cost of (a): a person confirms work of an agent that they stopped. Cost of (b) and (c): lost work. Decides: the owner.
4. **What is the error code for a unit that the delegation does not cover?** Options: (a) reuse the standard 403 of the authorization check; (b) a NEW code. Decides: the owner.
5. **What credential does Dusty use to write the draft?** RFC 121 decision 10 says Dusty has no delegated token. Options: (a) the run holds the delegation ID and the server checks the grant of the delegation at each call; (b) a short-lived token bound to the run. Cost of (b): a second credential. Decides: the owner. The earlier draft took (a) as a provisional answer for the unit check and named no source.

Questions considered and answered:

- May Dusty approve? No. ADR 299 decision 35.
- Who is the proposer? The person (RFC 121 decision 26).
- What was the earlier route and action for the confirm of the draft? They are withdrawn. No line names them.
