---
version: 6
---

Issue: #1253.

## Why

**Persona: developer.** A Developer deploys a stack or workspace. A gate in error in enforce mode stops every write of its scope until a person saves the gate again. This change builds the decision layer of a narrow, audited break-glass for that error. The store, the routes and the proof kind move to the follow-up change break-glass-pass-wiring. Until that change, a request that sets break-glass still fails closed.

The user path this change must make work:

1. **CLI.** Developer runs the Pulumi CLI or OpenTofu. A gate in enforce mode errors at the write and the write is refused. This stays true after this change.
2. **API.** Developer reads the gate in error with GET /api/dustpan/v1/namespaces/{namespace}/gates/{id}.
3. **Docs page.** The docs page says that break-glass for a gate in error is not reachable yet and that the write stays refused.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Gate in error stops the write | Write refused: a gate is in error. Read the gate and save it again. | `GET /api/dustpan/v1/namespaces/{namespace}/gates/{id}` |
| A change sets break-glass before the pass store is wired | Break-glass is unavailable (gate.unavailable). The write is refused and no pass is used. | `GET /api/dustpan/v1/namespaces/{namespace}/gates/{id}` |

Evidence for the claims above:

- Change.BreakGlass is not implemented on main. Proof: Issue #1253 body: a change that sets it returns gate.ErrUnavailable. The pack rg for 'Change.BreakGlass' outside tests found 0 matches.
- The issue asks for the action namespace:break-gate-error, a reason, a fresh authentication, an audit entry of the kind override and a 15-minute single-use pass. Proof: Issue #1253 body: the action `namespace:break-gate-error`, a reason, a fresh authentication, an audit entry of the kind `override`, and a pass of 15 minutes for the same principal, unit and operation kind, used once.
- A request that sets break-glass fails closed today. Proof: Issue #1253 body says a change that sets Change.BreakGlass returns gate.ErrUnavailable; pack code_facts list gate.ErrUnavailable at pkg/gate/gate.go:76.

## What Changes

- NEW: in pkg/gate, an unexported pass type with one constructor. It checks the reason (1 to 500 characters, no control characters), the operation kind, the holder and the unit stable ID.
- NEW: a decision function that passes only a gate in error in enforce mode, for the same holder, unit stable ID and operation kind, within 15 minutes, once.
- NEW: a store port for passes with a conditional consume. It has no adapter. Its default answers gate.ErrUnavailable, so the routes stay fail-closed.
- Deny, warn and approval outcomes stay unchanged. A deny with break-glass still refuses.

Out of scope:

- The pass store adapters and their baseline place (follow-up change break-glass-pass-wiring)
- The audit writer for the override entry (break-glass-pass-wiring)
- The grant check point for namespace:break-gate-error (break-glass-pass-wiring)
- The route fields on the Pulumi update create route and the TFE-compatible state-version routes (break-glass-pass-wiring)
- The proof kind of the fresh authentication (break-glass-pass-wiring)
- Break-glass for deny gates
- Editing or disabling the gate in error
- Change-order decision points

Depends on:

- none

Removed from Depends on (14 Nov 2026, not a code dependency):

- break-glass-pass-wiring: the pass wiring builds on this change; task 4.1 stays BLOCKED by it

## Usability

Where the path still costs the person time or surprises them:

- Break-glass is not reachable until the follow-up change. The docs page says so, so a Developer does not look for a missing flag.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- gate.ErrUnavailable exists (`pkg/gate/gate.go:76`)
- The enforce path of the state write run exists (`pkg/gate/statewrite.go:618 (func stateWriteRun.enforce)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 13).

- Decisions that apply: 13 (current), 17 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`. It is never an allow and never a deny. The record has the result `error` and the Cedar message."
- Quote: "Break-glass accounts meet the gates. A session of a break-glass account (ADR 70) is outside the access limits (ADR 223 decision 7). Gates, approval rules and fixed controls hold for each change of such a session."
- Effect: The default stays fail closed. The new decision layer adds only the narrow error pass, and the pass has no store behind it until the follow-up change.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A break-glass for a `deny` gate, with a review after use, is a later feature. Until a later ADR accepts it, the break-glass of ADR 224 decision 13 passes the gates in error only."
- Effect: The decision layer passes a gate in error only. A deny never passes.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does break-glass pass a deny gate? No. ADR 335 decision 2 limits the pass to gates in error.
- Do the store, audit, grant check and route fields belong here? No. No ADR in the pack decides their shape, so they move to break-glass-pass-wiring. This change stays in the decision layer.
- Does a request that sets break-glass pass before the wiring? No. The new port fails closed with gate.ErrUnavailable, as ADR 224 decision 13 requires for errors.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
