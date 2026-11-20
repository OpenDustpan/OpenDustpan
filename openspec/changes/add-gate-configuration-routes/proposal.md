---
version: 9
---

Issue: #1003, #1034, #1035, #1159.

## Why

**Persona: namespace administrator, and operator for the installation scope.** A namespace administrator sets who may change a stack or a workspace and who must approve. On main, the gate and access limit routes exist at the namespace, collection and unit scopes. The approval default and the approval rules have routes at the namespace scope only. The installation holds gates, access limits and approval rules in the store, but no route reaches them, and the configuration service refuses the installation scope. So an administrator cannot set an approval rule for one collection or one stack, and an operator cannot read the rules that act on all namespaces.

The user path this change must make work:

1. **Docs page.** Open the gate configuration reference page, find the row of the target scope and copy the call.
2. **API.** List, read, save and delete approval rules, and read, save and reset the approval default, at the collection and the unit scope. Read the gates, the access limits and the approval rules of the installation.
3. **CLI.** Run the same calls through the client that is generated from the API description.
4. **E2e.** The suite calls the routes through the real server on each store, as it does for the namespace routes. No CLI sets a gate or an approval rule, so the harness sends the requests. It signs in as a site administrator for a write, because an access token never gives a fresh authentication.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A principal without the read or manage action calls a route. | 403 with code access.denied. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-rules` |
| A read names an object of another scope or a missing ID. | 404 with code gate.not-found. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/collections/payments/approval-rules` |
| A write names a stack or a workspace that does not exist. | 404 with code unit.not-found. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/units/by-name/pulumi/web%2Fprod/approval-rules` |
| A write carries a stale version in If-Match. | 409 with code gate.version-conflict. | `curl -s -i -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/collections/payments/approval-default` |
| A narrower scope saves a rule with the ID of a wider final rule. | 409 with code gate.final. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-rules` |
| A save is above a scope limit. | 422 with code gate.limit. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-rules` |
| A change comes from an access token or an old sign-in. | 403 with code mfa.fresh-step-required. | Sign in to the console again, then repeat the call with the session. |

## What main already does

Each fact names its file and line on main. This section was checked on main at commit 8c76edc53 (19 Nov 2026). The change specifies only the difference.

- The routes of gates and access limits exist at the namespace, collection and unit scope, each with list, get, set and delete. (`internal/api/server/gate_objects.go:66-72 (func gateObjectScopes)`, `internal/api/server/gate_objects.go:91 (func Handler.gateObjectRoutes)`)
- Tests prove them: `TestGateRoutesLifecycle` (`internal/api/server/gate_objects_test.go:62`), `TestGateRoutesRefusals` (:147), `TestGateRoutesAuthorization` (:330), `TestGateRoutesRefuseAccessTokenWrites` (:347), `TestGateRoutesFollowTheUnit` (:1001). The e2e step `TestGateRoutes` (`e2e/gate_routes_test.go:29`) calls them through the real server. It does not call them through the Pulumi CLI: the comment of the test says "no CLI sets a gate".
- The approval default has GET, PUT and DELETE at the namespace scope, and the approval rules have list, get, set and delete at the namespace scope only. (`internal/api/server/gate_config.go:88-90` for the paths, `internal/api/server/gate_config.go:296-320` for the rows)
- The reset of the approval default is idempotent. It answers 204 also when none is set. (`internal/api/server/gate_config.go:404-434 (func Handler.removeApprovalDefault)`, test `TestApprovalDefaultLifecycle` at `internal/api/server/gate_config_test.go:346`)
- The handlers of the approval default and the approval rules build `gate.NamespaceScope(obj.Namespace())`. The gate handlers build the scope from the path with `gateScopeOf`. (`internal/api/server/gate_objects.go:253 (func gateScopeOf)`)
- Each write route runs behind `h.fresh`. All writes of gates, access limits, approval rules and approval defaults need a fresh authentication, not only the writes of an enforce gate or a final gate. An access token never gives it. (`internal/api/server/freshness.go:212 (func Handler.fresh)`, tests `TestGateConfigNeedsFreshSignIn` at `internal/api/server/gate_config_test.go:1148` and `TestGateConfigRefusesAccessTokenWrites` at :1183)
- A PUT without If-Match replaces the stored object, or creates it. A DELETE without If-Match deletes the stored version. A stale If-Match answers 409 gate.version-conflict. No gate route answers 428 for a missing If-Match. (`internal/api/server/gate_config.go:623-648 (func Handler.expectedVersion, func Handler.existingVersion)`, `docs/reference/gate-config-api.md:83-87`, tests `TestApprovalDefaultIfMatchCodes` at `internal/api/server/gate_config_test.go:443` and `TestApprovalRuleIfMatchCodes` at :553)
- The service refuses a gate, an access limit, an approval rule and an approval default at the installation scope with `gate.ErrInvalid`. (`pkg/gate/config.go:312 (func requireNamespaceScope)`) Its `prepare` reads the fence of a namespace only. (`pkg/gate/config.go:325 (func ConfigService.prepare)`)
- The store already accepts the installation scope for every object kind. The fence of the installation is the zero fence. (`pkg/store/internal/gatestore/objects.go:36-55 (func checkFence, func checkFenceOf)`, `pkg/gate/scope.go:92 (func InstallationScope)`, `pkg/gate/scope.go:286-295 (func Scope.HoldsConfig, func Scope.HoldsApprovalDefault)`) The conformance suite runs each kind at each scope. (`pkg/gate/gatetest/run.go:383 (func eachKind)`)
- The stable unit key is tested at the port: a reused name starts clean, a rename and a move keep the rows, a deleted scope is not found, and a scope with a unit ID reads the deleted unit. (`pkg/gate/gatetest/run_units.go:34-41`, `:135 (func caseReusedNameClean)`, `:163 (func caseDeletedScopeNotFound)`, `:185 (func caseRenameKeeps)`, `:291 (func caseIDScopeReadsDeleted)`)
- The action `installation:read-gates` exists, and the operator family holds it. The action `installation:manage-gates` does not exist. (`pkg/identity/identity.go:265 (const ActionInstallationReadGates)`, `pkg/access/access.go:122`; `rg -n "manage-gates" pkg/identity/identity.go` gives only `namespace:manage-gates` at :198)
- The catalog of actions has 128 entries. (`pkg/identity/actions_test.go:14 (const catalogCount)`)
- The error codes exist. `classifyGateConfig` maps gate.not-found, gate.version-conflict, gate.final, gate.limit, gate.invalid, gate.unavailable and unit.not-found. (`internal/api/server/gate_config.go:652 (func classifyGateConfig)`) The code mfa.fresh-step-required is in `internal/errcode/catalog_mfa.go:15`. A caller without the action gets 403 access.denied (`docs/reference/gate-config-api.md:194`), not gate.denied.
- A reference page and a guide exist. (`docs/reference/gate-config-api.md`, `docs/guides/approval-rules.md`) The reference page lists the routes of gates and access limits only. The guide lists the approval routes of a namespace only.
- The e2e coverage gate lists each route that no CLI drives, with the test that proves it. (`e2e/coverage_gate.go:86-130 (func approvalRuleAllowEntries, func gateObjectAllowEntries)`) The e2e fake `coverageGateConfig` implements `GateConfig`. (`e2e/coverage_gate.go:16`)
- The store port has `CarryOver` and `PutCarryOver`, and `ConfigService` and the server API have no method and no route for them. (`pkg/gate/store.go:154-170`, `pkg/store/internal/gatestore/settings.go:173`, `rg -n "signature-carry-over" internal pkg e2e --glob '!*_test.go'` gives no match) This change does not add them. See "Parked: the carry-over setting".

## What Changes

- NEW routes at the collection and the unit scope: list, get, set and delete of approval rules, and get, set and reset of the approval default. They use `gateScopeOf`, the actions `namespace:read-gates` and `namespace:manage-gates`, and the same fresh authentication, If-Match and error rules as the namespace rows.
- NEW service support for the installation scope in `gate.ConfigService`: gates, access limits and approval rules, with the zero fence and the audit scope `@server`.
- NEW read routes at the installation scope: GET of the list and GET of one object for gates, access limits and approval rules. They declare `installation:read-gates`.
- Docs: the rows of the new routes in `docs/reference/gate-config-api.md` and `docs/guides/approval-rules.md`.
- Regenerated API description and clients, and the e2e rows.

Parked (no task of this change builds them until the owner answers):

- Owner question 1: the action `installation:manage-gates` and the PUT and DELETE routes at the installation scope (tasks 4.1 to 4.3).
- Owner question 2: the signature carry-over setting, its service methods and its routes (no task). ADR 299 decision 12, an owner decision, deletes the setting and replaces it with the binding level. The routes that ADR 230 lists for it would serve a setting that is to be deleted.

Out of scope:

- The 428 answer for a PUT without If-Match. Main replaces the stored object and its reference page says so. A change of that rule changes the contract of routes on main. It needs its own decision (see "Questions considered").
- The save of a gate while locks are held, with the explicit flag of ADR 335 decision 5 (owner, 7 Nov 2026). No open change builds it: `rg -n "held lock|locks are held" openspec/changes --glob '*/tasks.md'` gives no task that builds it. The change that adds it must name the flag, and no ADR names it.
- Ownership rule routes and path set routes (own changes).
- The decision record read route (merged).
- Decision points on tool paths, the gate simulate route and the `gates/check` route.
- Profile gates and blueprint gates.
- The console policy pages.
- A change of the rule that every gate write needs a fresh authentication. Main is stricter than the ADR, which asks for it for an enforce gate. This change keeps main.

Depends on:

- none

Builds on (not a change):

- #996
- #1015

## Usability

Where the path still costs the person time or surprises them:

- The docs page gives one copy-paste call for each scope and each object kind.
- A stale version error says that the read call gives the current version in the ETag header.
- The page says that an access token never gives a fresh authentication, and what to do instead.
- The page says that a list shows the objects stored at its own scope only, so a collection list does not show the rules of the namespace.

## Owner decisions that apply

- **ADR 216 decision 1 (owner, 18 Oct 2026):** "Operator scope and namespace scope (owner)." It sets the rule for which installation action goes to the operator. See owner question 1.
- **ADR 299 decision 12 (owner, 1 Nov 2026):** "The binding level takes over signature carry-over (owner. Details by the PM)." See owner question 2.
- **ADR 335 decision 5 (owner, 7 Nov 2026):** "A save needs a flag when locks are held". This change does not build it (see "Out of scope").

## ADR check

Each quote is copied from the file in `docs/adr/`. Owner tags and "Changed by" chains are from the files and from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (owner-tagged: decision 1; decision 3 is marked "(PM)").

- Decisions that apply: 1, 2, 3 (amended by ADR 268 and ADR 355), 4 (extended by ADR 335 and ADR 355), 5 and 6.
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote (decision 1): "In the server, through the API (owner). Gates, access limits, approval rules, approval defaults, ownership rules, path sets and the signature carry-over setting are stored in the server."
- Quote (decision 2): "`installation:manage-gates` | the installation | gates, access limits, approval rules, ownership rules, path sets and the signature carry-over setting of the installation".
- Quote (decision 2): "`installation:read-gates` | the installation | read the gates, limits, approval rules, ownership rules and path sets of the installation".
- Quote (decision 3): "`GET`, `PUT .../approval-default` | read, manage" (the row for `{scope}`) and "`GET .../gates`, `GET .../gates/{id}`, and the same for `.../access-limits`, `.../approval-rules`, `.../ownership-rules` and `.../path-sets` | `installation:read-gates`".
- Quote (decision 3): "A list route gives the objects stored at that scope, not the objects of wider scopes. A `PUT` names no scope in its body, and refuses an unknown field."
- Quote (decision 6): "A scope holds at most 256 gates, 64 access limits, 64 approval rules, 256 ownership rules and 64 path sets. A condition holds at most 4 KiB."
- Effect: The change serves the missing rows of decision 3 for approval rules and approval defaults, and the installation read rows. The installation write rows wait for owner question 1. The carry-over rows wait for owner question 2. Decision 2 asks for a fresh authentication for the change of an approval rule, an approval default, an access limit and an enforce gate. Main already asks for it for every write of these objects, and the change keeps main. The ADR sets a minimum, so this is not a conflict.
- Conflict: ADR 230 decision 2 and 3 name the signature carry-over setting. ADR 299 decision 12 (owner) deletes it, and ADR 230 has no "Changed by" item for ADR 299. The change does not build those rows (owner question 2).

**ADR 268: Approval-default reset route and gate read roles** (decided by the PM under the owner's delegation of 20 October 2026; not owner-tagged).

- Decisions that apply: 1 and 2.
- Changed by: none. Chain to the end: none.
- Current rule: ADR 230 with this ADR.
- Quote: "`DELETE {scope}/approval-default` resets the scope to no default rule. The route is idempotent and returns 204, also when the scope has no default. It declares the same action as `PUT {scope}/approval-default`."
- Quote: "The namespace roles viewer, auditor, writer and maintainer hold `namespace:read-gates`. Of the namespace roles, only the administrator holds `namespace:manage-gates`."
- Effect: The reset route of the collection and the unit scope has the same rule as the namespace route, which main already has.
- Conflict: none.

**ADR 355: Gate configuration paths and errors as shipped** (the status line records acceptance by the project owner on 7 November 2026).

- Decisions that apply: 1 and 2.
- Changed by: none. Chain to the end: none.
- Current rule: this ADR, with ADR 230.
- Quote: "The unit scope of the gate routes is `/api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}`. The segment `by-name` is part of the path."
- Quote: "A gate configuration error that wraps `gate.ErrNotFound` has the status 404 and the code `gate.not-found`."
- Effect: Every new route uses the by-name unit path and these error rows.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 12).

- Decisions that apply: 12.
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Final gates (owner, 2026-10-18)."
- Effect: A narrower scope cannot change a wider final object. The service refuses it with `gate.ErrFinal`. This is service behavior of main that the new scopes reuse.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes** (owner-tagged: decision 1, and the title).

- Decisions that apply: 1 and 2.
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273.
- Current rule: this ADR, with ADR 273.
- Quote: "**Operator scope and namespace scope (owner).** The operator scope is the installation object `@server`."
- Quote: "An action has operator scope when it changes how the whole installation runs, or when it reads or writes data of more than one namespace."
- Effect: Installation gates act on every namespace, so the rule points to the operator family for the new action. The list of installation actions in this ADR has no gate action, so no ADR decides which role holds `installation:manage-gates`. This is owner question 1.
- Conflict: none.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged: decision 12).

- Decisions that apply: 12.
- Changed by: ADR 328 extends decision 1; ADR 359 extends decisions 1, 15 and 25; ADR 368 changes decision 19. Chain to the end: ADR 328, ADR 359, ADR 368. None of them touches decision 12.
- Current rule: this ADR, with ADR 328, 359, 368.
- Quote: "The binding level takes over signature carry-over (owner. Details by the PM)."
- Quote: "The setting `signatureCarryOver` has no users and no stored data. It is deleted with no conversion."
- Quote (consequences): "The code of `signatureCarryOver` is deleted in its own pull request."
- Effect: Carry-over routes would serve a setting that this decision deletes. The change builds none. This is owner question 2.
- Conflict: with ADR 225 decision 12 and ADR 230 decisions 2 and 3 for the carry-over rows. The "Changed by" chain of ADR 225 lists ADR 299 decision 12 as a change, so ADR 225 is already changed. ADR 230 is not marked.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1.
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The rows of the new routes ship in the same pull request.
- Conflict: none.

**ADR 250: Defense in depth** (owner-tagged).

- Decisions that apply: all.
- Effect: Each new write path has its hostile table for the handler, the service and the port adapter (see the design).
- Conflict: none.

## Open questions

Open questions: 2. Each one parks only the part that the text names. Tasks 0 to 3 and 5 do not depend on either answer.

1. **Owner.** Which built-in roles hold `installation:manage-gates`, and is it in the operator family? No ADR decides it. Decision 2 of ADR 230 names the action and the object, and the list of installation actions in ADR 216 has no gate action. Precedent: `installation:read-gates` and `installation:store-check` are in the operator family. The rule of ADR 216 decision 2 also points to the operator, because an installation gate acts on every namespace. The family decides which roles hold the action: the installation operator and the site administrator hold each action of the operator family. Parked: tasks 4.1 to 4.3.
2. **Owner.** Do the routes of the signature carry-over setting get built? ADR 230 (decisions 2 and 3) and ADR 225 decision 12 name the setting. ADR 299 decision 12 (owner) says that the binding level replaces it and that its code is deleted. Recommendation: build no route, add the "Changed by" item for ADR 299 to ADR 230, and give the binding level its own routes in the review-request change. Parked: no task builds carry-over until the owner answers.

Questions considered and answered:

- Are gate objects stored in the server? Yes. Decision 1 of ADR 230 and ADR 222 decision 6.
- Is the DELETE route of the approval default in scope for the collection and the unit scope? Yes. ADR 268 gives it for each scope, and it answers 204 also when none is set.
- Which key names the unit scope of gate rows? The stable unit ID, by the owner decision of 19 October 2026 in issue 1003. The path keeps the by-name form of ADR 355. Main does this already, and the conformance suite tests it. This change adds only tests that the new routes go through `gateScopeOf`.
- Does a PUT without If-Match answer 428? No on main. The earlier draft of this change said so, and no ADR asks for it. The reference page says that a PUT without If-Match replaces the stored object. This change keeps that rule.
- Does a route answer 403 with code gate.denied for a caller without the action? No. Main answers access.denied. The code gate.denied is for the refusal of a change by a gate.
- Is a new reference page needed? No. `docs/reference/gate-config-api.md` and `docs/guides/approval-rules.md` exist. The change adds rows to them.
- Does MinApprovals need removal work? No. `rg -n -F --glob '!*_test*' 'MinApprovals' pkg internal cmd` gives 0 matches.
- Do the installation read routes need the new action? No. `installation:read-gates` exists and is classified.
