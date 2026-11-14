---
version: 5
---

Issue: #1257.

## Why

**Persona: developer.** A Developer whose state write is refused needs to open the decision record that the refusal names, to see which gate refused and what to fix next.

The user path this change must make work:

1. **API.** Developer runs a state write through the tool API and gets a refusal that names a decision record ID.
2. **API.** Developer opens the record through the NEW read route to see which gate refused and why.
3. **Docs page.** Developer follows the docs page that shows the read call as written.
4. **E2e.** The suite runs a refused write with the real OpenTofu CLI, then reads the record with a personal access token.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller without the gate read action, a caller with no namespace access, or an unauthenticated caller reads a record | 404 with code gate.not-found and the same body as for an unknown ID. | `GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` (NEW route) |
| Any caller reads a missing, expired, malformed or foreign record ID | 404 with code gate.not-found and no record content. | `GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}` (NEW route) |

Evidence for the claims above:

- Issue #1257 asks for a read route of decision records with its actions. Proof: Issue #1257: A refusal at `state.write` names a decision record ID, but no route reads a record. Add the read route (ADR 229 decision 2) with its actions.
- The state.write decision point exists in the server code. Proof: The code fact `rg -n -F --glob '!*_test.go' 'state.write' pkg internal cmd` shows internal/api/stategate/stategate.go:2 and cmd/opendustpan/main.go:1811.

## What Changes

- NEW route GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}: read one record by gdr- ID with the namespace gate read action.
- NEW route GET /api/dustpan/v1/decision-records/{id}: read one record of an access limit on an installation object with installation:read-gates.
- Every denied read, and every read of a missing, expired, malformed or foreign ID, answers 404 gate.not-found with the same body.
- The state.write refusal and the approval refusal name the record ID and the read route, and name no change-order command until the change-order slice ships one.

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

- Decisions that apply: 3 (changed), 4 (extended).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "Routes (PM). Under the prefix of ADR 210, the scope is part of the path."
- Quote: "Errors. `pkg/gate` holds the sentinels, with these status codes and stable error codes (ADR 182):"
- Effect: New read routes sit under the ADR 210 prefix, declare the gate read actions and use the gate error codes.
- Conflict: none.

**ADR 355: Gate configuration paths and errors as shipped** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A gate configuration error that wraps `gate.ErrNotFound` has the status 404 and the code `gate.not-found`."
- Effect: Missing and denied records reuse the shipped 404 shape.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a denied read answer 403 or 404? 404, with the body of an unknown ID. ADR 215 decision 8: "A path that names a namespace that does not exist, or that the caller cannot read, answers 404. The answer holds no data of a namespace: no name, no ID and no count." The same rule is in ADR 154 decision 9 and ADR 166 decision 14, and in the owner rule against existence oracles.
- Does a read open plain secret values? No. Every read returns the stored masked form. No action opens plain values. PCI DSS requirement 3 keeps stored sensitive values unreadable.
- Which paths does the read use? A namespace route and an installation route, as ADR 229 decision 5 sets the data scope.
- What does a read give after retention expiry? ADR 229 decision 5 keeps state.write records for the audit retention. After that the read gives 404 gate.not-found.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
