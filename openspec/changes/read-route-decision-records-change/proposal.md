---
version: 2
---

Issue: #1257.

## Why

**Persona: developer.** A Developer whose state write is refused needs to open the decision record that the refusal names, to see which gate refused and what to fix next.

The user path this change must make work:

1. **API.** Developer runs a state write through the tool API and gets a refusal that names a decision record ID.
2. **API.** Developer opens the record through the new read route to see which gate refused and why.
3. **Docs page.** Developer follows the docs page that shows the read call as written.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller with namespace access but without the gate read action reads an existing record of the namespace | 403 with code gate.denied and the record ID. | `curl /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` |
| Any caller reads a missing, expired, malformed or foreign record ID, or a caller has no namespace access | 404 with code gate.not-found and no record content. | `curl /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` |

Evidence for the claims above:

- Issue #1257 asks for a read route of decision records with its actions. Proof: Issue #1257: A refusal at `state.write` names a decision record ID, but no route reads a record. Add the read route (ADR 229 decision 2) with its actions.
- The state.write decision point exists in the server code. Proof: The code fact `rg -n -F --glob '!*_test.go' 'state.write' pkg internal cmd` shows internal/api/stategate/stategate.go:2 and cmd/opendustpan/main.go:1811.

## What Changes

- New route GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}: read one record by gdr- ID with the namespace gate read action.
- New route GET /api/dustpan/v1/decision-records/{id}: read one record of an access limit on an installation object with installation:read-gates. A caller without the action gets 403 gate.denied. A missing, expired or malformed ID gets 404 gate.not-found.
- Each error names the record ID where the record is in the scope of the caller.
- The state.write refusal and the approval refusal name the record ID and the read route, and no change-order command, until the change-order slice ships one.

Out of scope:

- Write routes for decision records.
- The simulate route and starter packs.
- Console run page and CLI output for records.
- The real change-order command.

## Usability

Where the path still costs the person time or surprises them:

- A Developer finds the read call on the docs page from the record ID in the refusal.
- Until the change-order slice ships, the refusal names the read route only.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The state.write guard point exists in the server wiring. (`cmd/opendustpan/main.go:1811`)
- The state-gate adapter covers state.write for HTTP adapters. (`internal/api/stategate/stategate.go:2`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 229: Decision records, the simulate route and starter packs** (not owner-tagged).

- Decisions that apply: 2 (current), 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Every block is explained. Each refusal, each approval wait and each warning of a change names the ID of its record. The API error, the console and the CLI link to it."
- Quote: "Storage. A record is namespace data. The store checks the fence and writes it in the same transaction as the step that it explains."
- Effect: Adds the read side: stored records are read by ID, and the refusal links to the route. Installation limit records use the installation route.
- Conflict: none.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (changed), 4 (extended).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "Routes (PM). Under the prefix of ADR 210, the scope is part of the path."
- Quote: "Errors. `pkg/gate` holds the sentinels, with these status codes and stable error codes (ADR 182):"
- Effect: New read routes sit under the ADR 210 prefix, declare the gate read actions and use the gate error table.
- Conflict: none.

**ADR 355: Gate configuration paths and errors as shipped** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A gate configuration error that wraps `gate.ErrNotFound` has the status 404 and the code `gate.not-found`."
- Effect: Missing records reuse the shipped 404 shape.
- Conflict: none.

## Open questions

1. Does an in-scope caller without the read action get 403 gate.denied or 404 gate.not-found? Decides: PM.
   - Options: 403 for an in-scope caller without the action, 404 otherwise; 404 for every denied read.
   - Recommended: 403 for an in-scope caller without the action, 404 otherwise Reason: No ADR sets the disclosure rule for records.
2. Does a record read return only the stored masked form, or does a named secrets action open plain values? What does the stored fact hash reveal? Decides: PM.
   - Options: Stored masked form on every read, no plain-text open; A new secrets action that opens plain values.
   - Recommended: Stored masked form on every read, no plain-text open Reason: No quoted ADR names a secrets action for fact documents. The first slice adds no new action.

Not spec-ready: a code check fails: resolution R5.3 answers a missing scenario but adds no scenario: add the scenario and name it in the note; a code check fails: route `/api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` (.errors[0].command) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: task "E2e step, usability check and full check." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: and 2 more; question 2: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001.

Questions considered and answered:

- Which paths does the read use? A namespace route for namespace records and an installation route for installation limit records, as ADR 229 decision 5 sets the data scope.
- Does the record ID link cover waits and warnings? ADR 229 decision 2 requires it. This change adds the link where state.write answers; evaluation logic stays unchanged.
- What does a read give after retention expiry? ADR 229 decision 5 keeps state.write records for the audit retention. After that the read gives 404 gate.not-found.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
