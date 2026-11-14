---
version: 2
---

Issue: #1003, #1034, #1035, #1159.

## Why

**Persona: namespace administrator.** A namespace administrator sets who may change a stack or workspace and who must approve. On main, the gate configuration service exists and is wired, but the routes at collection, unit and installation scope are not all served. The administrator cannot set a policy for those scopes through the API.

The user path this change must make work:

1. **Docs page.** Open the gate configuration reference page and copy the route of the target scope.
2. **API.** List, read, save and delete gates and approval rules at a scope. Read, save and reset the approval default. Read and save the signature carry-over setting.
3. **CLI.** Run the same calls through the client that is generated from the API description.
4. **E2e.** Run the suite against the real binary with the real CLI client and a personal access token of a namespace administrator. It proves the routes, the 404 on another scope and the reuse of a unit name.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A principal without the read or manage action calls a gate route. | 403 with code gate.denied and the decision record ID. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates` |
| A read names an object of another scope or a missing ID. | 404 with code gate.not-found. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates` |
| A write carries a stale version in If-Match. | 409 with code gate.version-conflict. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates/deny-prod` |
| A save is above a scope limit. | 422 with code gate.limit. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/gates` |
| A narrower scope saves a rule with the ID of a wider final rule. | 409 with code gate.final. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-rules` |
| A change that needs fresh authentication comes from an access token. | The server refuses it with code mfa.fresh-step-required. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-default` |

Evidence for the claims above:

- The gate domain package exists. Proof: Pack path pkg/gate exists: true.
- The reference docs directory exists. Proof: Pack path docs/reference exists: true.
- A ConfigService is wired in the SDK and declared by the server API. Proof: rg gate.ConfigService gives pkg/dustpan/gateconfig.go:21 and internal/api/server/gate_config.go:24.
- The store has a delete of the approval default. Proof: rg Settings.DeleteApprovalDefault gives pkg/gate/gatetest/gatetest.go:634; the pack lists pkg/store/internal/gatestore/settings.go:129.
- No MinApprovals remains in non-test code. Proof: rg -n -F --glob '!*_test.go' 'MinApprovals' pkg internal cmd gives 0 matches, so no removal work is needed.
- The gate error codes exist in the catalog. Proof: The pack lists gate.not-found at internal/errcode/catalog_gate.go:31 and gate.version-conflict at internal/errcode/catalog_gate.go:36.
- The error code for a missing fresh step exists. Proof: The pack lists mfa.fresh-step-required at internal/errcode/catalog_mfa.go:15.
- The issues disagree on what is served, so the tests decide what is new. Proof: Issue 1003 says there are no routes; issue 1159 says slice 1 serves namespace approval routes only.
- No move of unit gate rows on rename is found in non-test code. Proof: rg -n -F --glob '!*_test.go' 'moveUnitGate' pkg internal cmd gives 0 matches.

## What Changes

- Gate, approval-rule and approval-default routes at installation, namespace, collection and by-name unit scopes, with read and manage actions of each scope
- The approval-default reset route, with 204 also when none is set
- The signature carry-over setting route at each scope that ADR 230 names
- Gate rows of a unit keyed on the stable unit ID, by an in-place edit of the baseline schema of each dialect
- Fresh authentication, stable error codes, audit entries and limits on each change
- Gates that name an approval rule as approval:<id>, resolved from the gate scope to wider scopes
- Backup and restore of gate configuration rows through the table list
- A reference page in docs/reference, and the generated client from make generate

Out of scope:

- Access limit routes (own change)
- Ownership rule routes (own change)
- Path set routes (own change)
- Decision record read route (own change)
- Decision points on tool paths and the gate simulate route
- Profile gates at profile.save and profile.open, and profile draft and access request workflows
- Taking change order approval requirements from rules (no MinApprovals remains on main)
- The console policy pages
- The default collection and catch-all placement rule at namespace create (namespace create change)

Depends on:

- #996
- #1015

## Usability

Where the path still costs the person time or surprises them:

- The docs page gives one copy-paste call for each scope.
- A stale version error names the read call that gets the current version.
- The page says that an access token never gives fresh authentication and what to do instead.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- gate.ConfigService is the service type of gate configuration. (`pkg/gate/config.go:69 (type ConfigService)`)
- The SDK builds the ConfigService for the server API. (`pkg/dustpan/gateconfig.go:21 (func newGateConfig)`)
- The server API declares the port that ConfigService implements. (`internal/api/server/gate_config.go:24 (type GateConfig)`)
- The store implements the delete of the approval default. (`pkg/store/internal/gatestore/settings.go:129 (func Settings.DeleteApprovalDefault)`)
- ConfigService has a delete of the approval default. (`pkg/gate/config.go:183 (func ConfigService.DeleteApprovalDefault)`)
- The error code for a missing fresh step is in the catalog. (`internal/errcode/catalog_mfa.go:15 (const mfa.fresh-step-required)`)
- The gate error codes are in the catalog. (`internal/errcode/catalog_gate.go:31 (const gate.not-found)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 3 (changed), 4 (extended), 5 (current), 6 (current), 7 (current).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "In the server, through the API (owner). Gates, access limits, approval rules, approval defaults, ownership rules, path sets and the signature carry-over setting are stored in the server."
- Quote: "A scope holds at most 256 gates, 64 access limits, 64 approval rules, 256 ownership rules and 64 path sets."
- Effect: The change serves the routes, actions, errors, audit entries, limits and backups of these decisions for gates, approval rules, approval defaults and the carry-over setting.
- Conflict: none.

**ADR 268: Approval-default reset route and gate read roles** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Approval-default reset route and gate read roles"
- Effect: The change adds the DELETE route of the approval default, as this ADR decides.
- Conflict: none.

**ADR 355: Gate configuration paths and errors as shipped** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The unit scope of the gate routes is `/api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}`. The segment `by-name` is part of the path."
- Quote: "A gate configuration error that wraps `gate.ErrNotFound` has the status 404 and the code `gate.not-found`."
- Effect: Every route, the docs page and the tests use the by-name unit path and these two error rows.
- Conflict: none.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decision 1, 4).

- Decisions that apply: 1 (changed), 4 (current).
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12. Chain to the end: ADR 286, ADR 299, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299.
- Quote: "Approval defaults (owner). An approval default can be set on a namespace, a collection or a unit."
- Effect: The routes store approval defaults and rules. Rule ID resolution goes from the gate scope to wider scopes. Requirement evaluation does not change here.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 12).

- Decisions that apply: 12 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Final gates (owner, 2026-10-18)."
- Effect: A narrower scope cannot change a wider gate. The service refuses it with gate.ErrFinal.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Gate decisions: deny gates, held locks, unlocked declares and the engine port"
- Effect: A save of a gate while locks are held answers 409.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place. It adds no numbered migration."
- Effect: The stable-ID key is an in-place edit of the baseline of each dialect, with no numbered migration and no conversion code.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The reference page ships in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Are gate objects stored in the server? Yes. ADR 230 decision 1 and ADR 222 decision 6.
- Is the DELETE route of the approval default in scope? Yes. ADR 268 adds it. The route resets to no default and answers 204 also when none is set.
- Which key names the unit scope of gate rows? The stable unit ID, by the owner decision of 19 October 2026 in issue 1003 and the rule of ADR 362. The path keeps the by-name form of ADR 355 decision 1, and the server resolves the name to the ID.
- Is the default collection at namespace create part of this change? No. Owner decision, 13 Nov 2026 (queue P5) puts it in the namespace create transaction. These routes let an administrator list and delete such gates and rules.
- Do access limits, ownership rules, path sets and decision records belong here? No. Other changes of this milestone own them.
- Does MinApprovals need removal work? No. The rg of non-test code gives 0 matches.
- How is a race on If-Match handled? The store compares the version inside the write transaction, so one writer wins.
- Which changes need fresh authentication? A save or delete of a final gate, of an approval default and of an approval rule. A session needs a sign-in inside the fresh window. An access token never counts as fresh and gets mfa.fresh-step-required.
- Do backups hold gate configuration? Yes. ADR 230 decision 7 says a backup holds these rows, so the table list names them.
- Do issues 1034 and 1035 need work here? No. No MinApprovals remains on main, and profile gates are another change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
