---
version: 2
---

Issue: #1253.

## Why

**Persona: developer.** A Developer deploys a stack or workspace. A gate in error in enforce mode stops every write of its scope until a person saves the gate again, with no path forward. The Developer needs a narrow audited break-glass to pass the error once and ship the fix.

The user path this change must make work:

1. **CLI.** Developer runs the Pulumi CLI or OpenTofu against the server; a gate in enforce mode errors at the write and the write is refused.
2. **API.** Developer reads the gate in error with GET /api/dustpan/v1/namespaces/{namespace}/gates/{id}.
3. **Docs page.** Developer retries the write with break-glass, a reason and fresh authentication as the docs page describes.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Gate in error stops the write | Write refused: a gate is in error. Read the gate, then retry with break-glass and a reason. | `GET /api/dustpan/v1/namespaces/{namespace}/gates/{id}` |
| Break-glass without grant or fresh auth | Break-glass refused: missing grant, bad reason or stale authentication. No pass is used. Read the gate. | `GET /api/dustpan/v1/namespaces/{namespace}/gates/{id}` |

Evidence for the claims above:

- Change.BreakGlass is not implemented today. Proof: Issue #1253 body: a change that sets it returns gate.ErrUnavailable. The pack rg for 'Change.BreakGlass' outside tests found 0 matches.
- The override audit kind, reason, fresh authentication and 15-minute single use come from the issue. Proof: Issue #1253 body: the action `namespace:break-gate-error`, a reason, a fresh authentication, an audit entry of the kind `override`, and a pass of 15 minutes for the same principal, unit and operation kind, used once.

## What Changes

- NEW: optional break-glass fields (reason, fresh-auth proof) on the Pulumi update create route and the TFE-compatible state-version route; break-glass passes a gate in error at the state.write decision point, with action namespace:break-gate-error and an audit entry of kind override.
- NEW: a 15-minute pass for the same holder, unit stable ID and exact operation kind, used once with a conditional consume in the same transaction as the write. The decision record keeps result error with a break-glass marker.
- Deny, warn and approval outcomes stay unchanged; a deny with break-glass still refuses.

Out of scope:

- Break-glass for deny gates
- Editing or disabling the gate in error
- Change-order decision points
- Pass list and revoke API and pass retention
- Effective-value view beyond the docs page

## Usability

Where the path still costs the person time or surprises them:

- A gate in error gives no path forward today; the error names break-glass and the gate read.
- The 15-minute single-use limit may surprise retry scripts; the refusal names expiry and use.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The job spec has a BreakGlass type (`pkg/job/job.go:403 (func Spec.Changes)`)
- gate.ErrUnavailable exists (`pkg/gate/gate.go:76`)
- The enforce path of the state write run exists (`pkg/gate/statewrite.go:618 (func stateWriteRun.enforce)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 13).

- Decisions that apply: 13 (current), 17 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`. It is never an allow and never a deny. The record has the result `error` and the Cedar message. An error of a gate in `evaluate` mode is in the record and st"
- Quote: "Break-glass accounts meet the gates. A session of a break-glass account (ADR 70) is outside the access limits (ADR 223 decision 7). Gates, approval rules and fixed controls hold for each change of such a session."
- Effect: Follows the fail-closed rule; adds the narrow error-only pass. Sessions of break-glass accounts stay under the gates.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A break-glass for a gate comes later (owner, 2026-11-07). A break-glass for a `deny` gate, with a review after use, is a later feature. Until a later ADR accepts it, the break-glass of ADR 224 decision 13 passes the gates in error only."
- Effect: Implements the error pass only; deny gates get no pass.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Add an e2e step with the real Pulumi CLI." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph.

Questions considered and answered:

- Does break-glass pass a deny gate? No. ADR 335 decision 2 limits the pass to gates in error.
- What happens today when Change.BreakGlass is set? The issue says it returns gate.ErrUnavailable; ADR 224 decision 13 requires fail closed, which this change keeps except for the pass.
- Does the pass cover every operation kind? No. The pass key holds the exact operation kind.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
