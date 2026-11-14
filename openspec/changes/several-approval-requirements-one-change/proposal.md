---
version: 2
---

Issue: #1158.

## Why

**Persona: developer.** A developer reviews a change that two rules guard, for example 2 of group platform plus 1 of any reviewer, and the apply must wait for both with different people.

The user path this change must make work:

1. **Docs page.** Set a namespace approval rule and a collection approval rule from the docs page
2. **API.** Start a job and read which requirements wait
3. **CLI.** Approve from two people and apply

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 403 apply waits: requirement 2 of group platform needs one more distinct approver | The server refuses the apply and names each unmet requirement with the missing distinct-approver count. Read the gate list to see what is missing. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| 403 approval refused: fresh authentication expired 300 seconds ago | The server refused the approval because the sign-in is too old. Sign in again in the console, then approve again. Read the gate list to see the window value. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |

Evidence for the claims above:

- The decider today returns an unavailable error for shapes beyond one group requirement. Proof: pkg/gate/jobgate/jobgate.go:166
- The store already persists freshAuthSeconds on approval rules. Proof: pkg/store/internal/gatestore/codec.go:38
- Issue #1158 asks for several requirements with bipartite matching of distinct approvers. Proof: Several requirements on one job or change order, with the bipartite matching of distinct approvers.

## What Changes

- Decider supports more than one approval requirement with distinct approvers by maximum matching
- Users and unit owners count as approvers by stable ID with recheck at apply time
- Fresh authentication window enforced per approval with revocation check
- Unless exemption evaluated by the gate engine with fail-closed error handling
- Apply refusal names each unmet requirement and the missing distinct-approver count

Out of scope:

- Gate configuration routes and starter packs
- Simulate route
- Console run page
- Fresh authentication window semantics, defined by fresh-authentication-window-0-turns
- Dev-data approver rules, defined by gate-evaluation-must-apply-dev
- Unless body authoring UI; the unless body is stored through the existing gate set routes by the rule setter

Depends on:

- fresh-authentication-window-0-turns
- gate-evaluation-must-apply-dev
- `gate-evaluation-must-apply-dev` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The refusal names which requirement is unmet and how many more distinct approvers it needs.
- The 403 text states the fresh window value so the person knows to sign in again.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- jobgate.Decider returns an error wrapping gate.ErrUnavailable for unsupported approval shapes (`pkg/gate/jobgate/jobgate.go:166`)
- Gate store persists freshAuthSeconds on approval rules (`pkg/store/internal/gatestore/codec.go:38`)
- gate.ErrUnavailable signals an unavailable gate evaluation (`pkg/gate/gate.go:76`)
- State gate maps gate.ErrUnavailable to a stored refusal record (`internal/api/stategate/stategate.go:478`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every control is a setting that a team turns on (owner, 2026-11-07)."
- Quote: "The default keeps today's behavior (owner, 2026-11-07)."
- Quote: "Security rules stay (owner, 2026-11-07)."
- Effect: Approval rules stay opt-in. Single-requirement behavior is unchanged. Stored multi-requirement shapes move from start failure to evaluation only where a team stored rules, with each exemption written to the audit log.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the change force approvals on teams that set no rule? No. ADR 353 decision 1 says every control is a setting that a team turns on. The change evaluates only stored rules and adds no default rule.
- What approval shapes must the change cover? Issue #1158 lists bipartite matching of distinct approvers, users and unit owners, fresh authentication window per approval, and unless exemption. This change covers matching, users and owners, and unless. It reuses the window model of fresh-authentication-window-0-turns and the dev-data approver rules of gate-evaluation-must-apply-dev instead of redefining them.
- How does the unless exemption reach a verdict without a new engine? The decider calls the existing gate engine with the change fact document. An unless body that errors, times out after 5 seconds, or exceeds 64 KB means not exempt. No new engine is added. This change holds the single unless model; other approval changes use it.
- Which fresh window applies when requirements set different freshAuthSeconds? The strictest assigned window applies. An approval counts toward a requirement only when its sign-in age fits the freshAuthSeconds of that assigned requirement. Maximum matching only pairs an approver with a requirement whose window the approver meets.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
