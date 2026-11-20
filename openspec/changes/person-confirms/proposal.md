Roadmap: `docs/roadmap.md:775` (A person confirms it).

## Why

**Persona: developer.** A developer asks Dusty to draft a change and then confirms it before anything applies. Work moves only after a person says yes, and four-eyes still holds.

The user path this change must make work:

1. **Console.** Open the draft from Dusty and read the plan.
2. **API.** Confirm the draft with a console session. Under a two-person rule, ask a second person to approve.
3. **API.** Read the audit log and see the confirmation entry before any apply entry.
4. **Docs page.** Follow `docs/guides/approval-rules.md` to set `authorMayApprove` to false.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| An agent tries to confirm | Refused and written as a denial entry. The error code is open question 4. | `GET /api/audit/entries` |
| The delegator tries a second approval under a two-person rule | 403 with the existing error code `access.denied`. The answer names the approval rule and the setting `authorMayApprove`. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-rules` |
| A confirm needs a fresh step and the caller has none | 403 with the existing error code `mfa.fresh-step-required`, only when `freshAuthEnabled` is on (ADR 385 decision 2). | The enrollment page |

Evidence for the claims above:

- Approval rules have e2e coverage on main. Proof: `e2e/approval_rules_test.go` and `e2e/state_write_gate_test.go`.
- The audit log has e2e and guide coverage on main. Proof: `e2e/audit_export_test.go` and `docs/guides/audit-log.md`.
- The error codes `access.denied` and `mfa.fresh-step-required` exist. Proof: `internal/errcode/errcode.go:19` and `internal/errcode/catalog_mfa.go:15`.
- No change order package, no confirm route and no change order action exist on main. Proof: `rg -n 'changeorder' -g '*.go' pkg internal` finds no such package. The earlier draft of this change named a NEW route `POST .../change-orders/{id}/confirm`, an action `changeorder:confirm`, and a NEW audit route `GET /api/dustpan/v1/namespaces/{namespace}/audit`. No ADR or RFC line names the first two. The third does not exist. The audit routes on main are `/api/audit/entries`, `/api/audit/entries/{sequence}`, `/api/audit/check` and `/api/audit/export` (`internal/api/audit/routes.go:49` to `:52`).

## What Changes

- Only a person confirms a draft of an agent. A request of an agent is refused and written as a denial entry (ADR 344 decision 2, RFC 121 decision 24).
- The person who delegated may confirm (ADR 354 decision 1).
- The person is the proposer, so four-eyes sees the person. Under `authorMayApprove` false the delegator is not a different second approver (RFC 121 decision 26).
- The confirmation writes its audit entry before any apply entry of the change (RFC 121 decision 51).
- When `freshAuthEnabled` is on, the confirm needs a fresh step (ADR 385 decision 2 lists "a confirmation"). An access token never gives a fresh step (ADR 227 decision 6).

Out of scope:

- The delegation record, in `person-delegates-dusty`.
- The draft, in `dusty-proposes-change-order`.
- The audit entry shape, in `every-step-audited`.
- The rules that bind a confirmation to a plan revision, the race of a new revision and a confirm, the cancel of a queued destroy, and the re-check of the grant of a confirmer. The earlier draft of this change specified them. They belong to the change order package, which is not on main, and this change could not verify an ADR line for each of them. They go to the change that makes the package.
- The console screens.

Depends on:

- `person-delegates-dusty`, `dusty-proposes-change-order` and `every-step-audited`.
- The change that makes the change order package. It is a dependency to be written.

## Usability

Where the path still costs the person time or surprises them:

- The person may not see why a second approval is refused under `authorMayApprove` false. The refusal names the rule and the setting.
- The delegator may expect one confirm to approve and apply. The console shows that a confirm and an approval are separate steps, if open question 2 keeps them separate.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a job gate status test for confirmation gates. (`pkg/job/gate_test.go:101 (TestConfirmationGateStatus)`)
- Main ships audit entries in order and moves cursors. (`pkg/audit/ship_test.go:258 (TestShipOnceCopiesEveryEntryInOrderAndMovesTheCursors)`)
- Main refuses a grant after a racing move. (`pkg/access/race_test.go:578 (TestGrantRefusedAfterARacingMove)`)
- Main leaves one entry and one operation for concurrent approvals. (`pkg/dustpan/auditchain_approval_test.go:112 (TestConcurrentApprovalsLeaveOneEntryAndOneOperation)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`.

**ADR 344: An agent drafts a change order, and a person confirms it** (owner-tagged).

- Decisions that apply: 1 and 2. Changed by: -. Chain to the end: none. Current rule: this ADR.
- Quote, decision 2: "The draft is not a change order until a person confirms it."
- Quote: "This ADR does not decide the state of a draft, the tool in ADR 122, the action name or the audit entry. A later ADR decides them."
- Effect: the action name, the state and the audit entry of a confirm are open questions here.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged) and **RFC 121** (accepted by ADR 354 decision 6).

- Quote, ADR 354 decision 1: "The person who delegated may confirm (owner, 2026-11-07)."
- RFC 121 decision 24: a change order that an agent drafts waits for the ADR 344 confirmation. Decision 26: the person is the proposer, and four-eyes sees the person. Decision 27: "The agent never holds step-up. An agent never has a fresh authentication."
- Current rule: this ADR. Conflict: none.

**ADR 190: Change orders: an object above jobs** (decisions 4 and 14 are owner-tagged).

- Decision that applies: 14 (changed by ADR 344). Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365. Chain to the end: ADR 222, 225, 299, 300, 344, 365, 286, 335, 351, 354, 328, 359, 377.
- Quote, decision 14: "An agent records no verdict: it submits its blocking threads in a review of the verdict `comment`."
- Effect: an agent records no verdict. Conflict: none.

**ADR 385: A fresh step is opt-in** (owner-tagged) and **ADR 227** (decisions 2 and 3 are owner-tagged).

- ADR 385 current rule: this ADR. ADR 227 current rule: this ADR, with ADR 301, 320, 339, 354, 373, 385.
- Quote, ADR 385 decision 2: "Off means no step (owner, 2026-11-15). When the setting is off, the server asks a person for no fresh authentication." The list that follows names "a confirmation".
- Quote, ADR 297 decision 18: "An access token never gives a fresh authentication (ADR 227 decision 6)."
- Quote, ADR 301 decision 4: "No factor, no fresh step (owner)."
- Effect: with the setting on, a confirm needs a fresh step, and a person with no factor cannot confirm. With the setting off, no step.
- Conflict: none.

**ADR 299: one integration with the Git host** (owner-tagged). Quote, decision 35: "Agents never approve, on either side (owner)." Current rule: this ADR, with ADR 328, 359, 368.

## Open questions

Open questions: 4, parked

The questions below are not decided. No ADR line and no RFC line settles them. The owner decides each one.

1. **What are the route, the action and the states of the confirm?** ADR 344 leaves the action name open, and no state of a draft is decided. The earlier draft invented a route, an action `changeorder:confirm` and the states `in-review` and `approved`. Options: (a) one action `changeorder:confirm` on the namespace; (b) the action on the unit. Cost of (b): a confirm of a plan with many units needs a grant on each. Decides: the owner.
2. **Does a confirm count as an approval, or are they two steps?** The earlier draft said that the delegator confirms and then approves, and that the second approval is refused under `authorMayApprove` false. Options: (a) two steps, as the earlier draft; (b) one step, and the confirm is the approval of the person. Cost of (a): a second click. Cost of (b): the confirm and the four-eyes rule share one entry. Decides: the owner.
3. **Which audit read route proves "no apply before confirm"?** Options: (a) `GET /api/audit/entries` with a filter on the object; (b) `GET /api/audit/export`. Cost of (b): the person reads a file and not a page. The scope that the caller needs is open too. Decides: the owner.
4. **What is the answer to an agent that confirms?** Options: (a) the standard 403 `access.denied`; (b) a NEW code `changeorder.agent-confirm-refused` as the earlier draft said. Cost of (b): one more code. Decides: the owner.

Questions considered and answered:

- May the person who delegated confirm? Yes (ADR 354 decision 1). A different second person is needed only when the setting `authorMayApprove` is false.
- May the agent confirm or approve? No (ADR 299 decision 35, RFC 121 decision 27).
- Is a check of the jobs route enough for "no apply before confirm"? No. The audit log must show it too (roadmap step 3).
