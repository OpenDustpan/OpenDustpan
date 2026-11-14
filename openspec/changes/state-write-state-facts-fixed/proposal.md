---
version: 2
---

Issue: #1258.

## Why

**Persona: developer.** A developer pushes state with the Pulumi CLI or OpenTofu and the write stops at the state.write gate, but the record shows state.available false, lists no fixed control and misses approval defaults, while access limits hold only at the state.write routes, so the developer cannot tell what the gate saw or what to fix.

The user path this change must make work:

1. **CLI.** Run pulumi up or tofu apply against the server from the CLI.
2. **API.** Read the 403 refusal with the record ID on a gate or limit denial.
3. **Docs page.** Open the record and fix the state, lock, approval or limit, then retry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A gate refuses the state write | 403 {"code": "gate.denied", "message": "state.write refused by gate <id>, record <gdr-id>. Run dustpan gates show <gdr-id>."} | `dustpan gates show <gdr-id>` |
| An access limit refuses the call on a unit, namespace, collection or installation object | 403 {"code": "gate.limited", "message": "access limit <id> refuses <action>, record <gdr-id>. Run dustpan gates show <gdr-id>."} | `dustpan gates show <gdr-id>` |
| A gate in enforce mode errors at state.write | 409 {"code": "gate.rule-failed", "message": "gate <id> error, record <gdr-id>. Run dustpan gates show <gdr-id>."} | `dustpan gates show <gdr-id>` |

Evidence for the claims above:

- The code text names state.available false when the state change has a reason Proof: pkg/gate/statewrite.go:284
- state.resources and state.removed facts come from the state-change input Proof: pkg/gate/statechange.go:28
- CheckAccess evaluates access limits and LimitedAuthorizer wraps an inner authorizer Proof: pkg/gate/service.go:297
- LimitedAuthorizer is wired in the job service only Proof: pkg/dustpan/jobservice.go:71

## What Changes

- Fill state.available, state.resources and state.removed for each tool-neutral operation that holds old and new state, with encrypted bodies mapped to available false and continued write.
- Write the allow record in the step transaction and the refusal record in its own transaction, and name the record ID in the audit entry.
- List workspace lock, manual lock, freeze, override switches and safe delete in the record.
- Apply approval defaults at state.write with distinct eligible approvers.
- Wire LimitedAuthorizer for every route and handle unit, namespace, collection and installation objects in CheckAccess.

Out of scope:

- New decision points
- Console run page
- Unit-import import object fact schema of ADR 241 decision 23
- Cross-requirement distinct-approver combining, in several-approval-requirements-one-change

Depends on:

- several-approval-requirements-one-change
- `add-access-limits-per-scope` (order of the milestone review)
- `several-approval-requirements-one-change` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refusal names the record ID and the one command that shows it, so the developer finds the gate without reading server logs.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The state-write guard sets state.available false when the state change is empty. (`pkg/gate/statewrite.go:382`)
- The facts state.resources and state.removed come from the state-change input. (`pkg/gate/statechange.go:28`)
- CheckAccess evaluates the access limits of the object chain. (`pkg/gate/service.go:297`)
- LimitedAuthorizer wraps an inner authorizer. (`pkg/gate/service.go:322`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 13, 16).

- Decisions that apply: 13 (current), 16 (changed), 17 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:"
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`."
- Quote: "Break-glass accounts meet the gates. A session of a break-glass account (ADR 70) is outside the access limits (ADR 223 decision 7). Gates, approval rules and fixed controls hold for each change of such a session."
- Effect: Evaluates state.write before each covered write; an engine error fails closed; a break-glass session skips access limits while gates hold.
- Conflict: none.

**ADR 229: Decision records, the simulate route and starter packs** (owner-tagged: decision 4).

- Decisions that apply: 4 (current), 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Fixed controls in the record (owner). The record shows the state of each fixed control that the server checked for the change: the workspace lock, a manual lock, an active freeze and its break-glass, the override switches, the module switches, the quota reservation and the quota counts, the source filter, the safe delete check of ADR 153 decision 5, the plan staleness check with the maximum plan age (ADR 102, ADR 191 decision 7), and the agent controls: the agent mode, the confirmation step and the secret consent."
- Quote: "The store checks the fence and writes it in the same transaction as the step that it explains."
- Effect: Lists each fixed control in the record; writes the allow record in the step transaction and the refusal record in its own transaction.
- Conflict: none.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "An evaluation writes no audit entry of its own. The audit entry of the step that it explains names the record ID."
- Effect: Names the record ID in the audit entry of the step and writes no audit entry for the evaluation.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An unlocked state version on the TFE-compatible API (owner, 2026-11-07). Such a write meets `state.write` before its transaction. Its operation kind is `unlocked-write`."
- Effect: Treats the TFE-compatible unlocked state version as a state.write operation of kind unlocked-write.
- Conflict: none.

**ADR 365: Wording refinements edit an ADR in place, with a changelog (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "With the owner's word ("A lgtm", 13 November 2026), decision 3 now says that such a state is not a read failure: `state.available` is `false`, the decision record says why, and the write goes on under the gates that apply."
- Effect: A client-encrypted or unreadable state gives state.available false with a reason and the write continues.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Run the e2e step against the real CLI, including a locked tofu apply." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>; a code check fails: task "Check wiring, route actions, e2e step, docs page and usability from th" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Must state.resources and state.removed have values when the operation holds both states, including encrypted bodies? Yes where both states are readable. ADR 365 decision 4 says an encrypted body gives state.available false with a reason while the write continues.
- Must allow and refusal records link from the audit entry with fixed controls listed? Yes. ADR 229 decisions 4 and 5 and ADR 230 decision 5 require it.
- Must access limits hold on every route and what is the break-glass exception? Yes. ADR 224 decision 17 skips limits for break-glass sessions while gates hold.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
