---
version: 9
---

Issue: #1003, #1034, #1035, #1159.

## Why

**Persona: namespace administrator, and operator (installation operator or site administrator) for the installation scope.** A namespace administrator sets who may change a stack or a workspace and who must approve. On main, the gate and access limit routes exist at the namespace, collection and unit scopes. The approval default and the approval rules have routes at the namespace scope only. The installation holds gates, access limits and approval rules in the store, but no route reaches them, and the configuration service refuses the installation scope. So an administrator cannot set an approval rule for one collection or one stack, and an operator can neither read nor set the rules that act on all namespaces.

The user path this change must make work:

1. **Docs page.** Open the gate configuration reference page, find the row of the target scope and copy the call.
2. **API.** List, read, save and delete approval rules, and read, save and reset the approval default, at the collection and the unit scope. Read, save and delete the gates, the access limits and the approval rules of the installation.
3. **CLI.** Run the same calls through the client that is generated from the API description.
4. **E2e.** The suite calls the routes through the real server on each store, as it does for the namespace routes. No CLI sets a gate or an approval rule, so the harness sends the requests. It signs in as a site administrator for a write, because an access token never gives a fresh authentication.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A principal without the read or manage action calls a route. This includes a holder of the namespace actions only, who calls an installation route. | 403 with code access.denied. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-rules` |
| A read names an object of another scope or a missing ID. | 404 with code gate.not-found. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/collections/payments/approval-rules` |
| A write names a stack or a workspace that does not exist. | 404 with code unit.not-found. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/units/by-name/pulumi/web%2Fprod/approval-rules` |
| A write carries a stale version in If-Match. | 409 with code gate.version-conflict. | `curl -s -i -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/collections/payments/approval-default` |
| A narrower scope saves a rule with the ID of a wider final rule. | 409 with code gate.final. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-rules` |
| A save is above a scope limit. | 422 with code gate.limit. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/namespaces/acme/approval-rules` |
| A change comes from an access token or an old sign-in. | 403 with code mfa.fresh-step-required. | Sign in to the console again, then repeat the call with the session. |

## What main already does

Each fact names its file and line on main. This section was checked on main at commit 344dec65f (20 Nov 2026). The change specifies only the difference.

- The routes of gates and access limits exist at the namespace, collection and unit scope, each with list, get, set and delete. (`internal/api/server/gate_objects.go:66-72 (func gateObjectScopes)`, `internal/api/server/gate_objects.go:91 (func Handler.gateObjectRoutes)`)
- Tests prove them: `TestGateRoutesLifecycle` (`internal/api/server/gate_objects_test.go:62`), `TestGateRoutesRefusals` (:147), `TestGateRoutesAuthorization` (:330), `TestGateRoutesRefuseAccessTokenWrites` (:347), `TestGateRoutesFollowTheUnit` (:1001). The e2e step `TestGateRoutes` (`e2e/gate_routes_test.go:29`) calls them through the real server. It does not call them through the Pulumi CLI: the comment of the test says "no CLI sets a gate".
- The approval default has GET, PUT and DELETE at the namespace scope, and the approval rules have list, get, set and delete at the namespace scope only. (`internal/api/server/gate_config.go:91-93` for the paths, `internal/api/server/gate_config.go:299-323` for the rows)
- The reset of the approval default is idempotent. It answers 204 also when none is set. (`internal/api/server/gate_config.go:407 (func Handler.removeApprovalDefault)`, test `TestApprovalDefaultLifecycle` at `internal/api/server/gate_config_test.go:348`)
- The handlers of the approval default and the approval rules build `gate.NamespaceScope(obj.Namespace())`. The gate handlers build the scope from the path with `gateScopeOf`. (`internal/api/server/gate_objects.go:253 (func gateScopeOf)`)
- Each write route runs behind `h.fresh`. All writes of gates, access limits, approval rules and approval defaults need a fresh authentication, not only the writes of an enforce gate or a final gate. An access token never gives it. (`internal/api/server/freshness.go:221 (func Handler.fresh)`, tests `TestGateConfigNeedsFreshSignIn` at `internal/api/server/gate_config_test.go:1150` and `TestGateConfigRefusesAccessTokenWrites` at :1185)
- A PUT without If-Match replaces the stored object, or creates it. A DELETE without If-Match deletes the stored version. A stale If-Match answers 409 gate.version-conflict. No gate route answers 428 for a missing If-Match. (`internal/api/server/gate_config.go:626 (func Handler.expectedVersion)`, `:646 (func Handler.existingVersion)`, `docs/reference/gate-config-api.md:83-86`, tests `TestApprovalDefaultIfMatchCodes` at `internal/api/server/gate_config_test.go:445` and `TestApprovalRuleIfMatchCodes` at :555)
- The service refuses a gate, an access limit, an approval rule and an approval default at the installation scope with `gate.ErrInvalid`. (`pkg/gate/config.go:327 (func requireNamespaceScope)`) Its `prepare` reads the fence of a namespace only. (`pkg/gate/config.go:352 (func ConfigService.prepare)`)
- The store already accepts the installation scope for every object kind. The fence of the installation is the zero fence. (`pkg/store/internal/gatestore/objects.go:36 (func checkFence)`, `:46 (func checkFenceOf)`, `pkg/gate/scope.go:92 (func InstallationScope)`, `pkg/gate/scope.go:286 (func Scope.HoldsConfig)`, `:293 (func Scope.HoldsApprovalDefault)`) The conformance suite runs each kind at each scope. (`pkg/gate/gatetest/run.go:383 (func eachKind)`)
- The stable unit key is tested at the port: a reused name starts clean, a rename and a move keep the rows, a deleted scope is not found, and a scope with a unit ID reads the deleted unit. (`pkg/gate/gatetest/run_units.go:34-41`, `:135 (func caseReusedNameClean)`, `:163 (func caseDeletedScopeNotFound)`, `:185 (func caseRenameKeeps)`, `:291 (func caseIDScopeReadsDeleted)`)
- The action `installation:read-gates` exists, and the operator family holds it. The roles `installation-operator` and `site-administrator` hold the operator family. The action `installation:manage-gates` does not exist in the catalog. (`pkg/identity/identity.go:266 (const ActionInstallationReadGates)`, `pkg/access/access.go:128`, `pkg/access/access_test.go:668` and `:676` for the two roles; `rg -n "manage-gates" pkg/identity/identity.go` gives only `namespace:manage-gates` at :199) The conformance suite already writes an installation entry with the action string `installation:manage-gates`. (`pkg/gate/gatetest/run.go:208`)
- The catalog of actions has 131 entries. (`pkg/identity/actions_test.go:16 (const catalogCount)`)
- The error codes exist. `classifyGateConfig` maps gate.not-found, gate.version-conflict, gate.final, gate.limit, gate.invalid, gate.unavailable and unit.not-found. (`internal/api/server/gate_config.go:659 (func classifyGateConfig)`) The code mfa.fresh-step-required is in `internal/errcode/catalog_mfa.go:15`. A caller without the action gets 403 access.denied (`docs/reference/gate-config-api.md:194`), not gate.denied.
- A reference page and a guide exist. (`docs/reference/gate-config-api.md`, `docs/guides/approval-rules.md`) The reference page lists the routes of gates and access limits only. The guide lists the approval routes of a namespace only.
- The audit action names of the gate writes are `<level>:<verb>-<kind>` strings, and every one of them has the prefix `namespace:`: `namespace:set-gate`, `namespace:delete-gate`, `namespace:set-access-limit`, `namespace:delete-access-limit` (`pkg/gate/config_objects.go:15-18`), `namespace:set-approval-default`, `namespace:delete-approval-default`, `namespace:set-approval-rule` and `namespace:delete-approval-rule` (`pkg/gate/config.go:114-117`). The comment of both blocks says that the value names the audit entry only and that the authorization action of the route is a different value. The installation actions that exist use the prefix `installation:` (`pkg/identity/identity.go:262`, `:266`). ADR 210 decision 7 renames the old area `server:` to `installation:` with the same verbs, and some code still has the old values.
- The e2e coverage gate lists each route that no CLI drives, with the test that proves it. (`e2e/coverage_gate.go:91 (func approvalRuleAllowEntries)`, `:127 (func gateObjectAllowEntries)`) The e2e fake `coverageGateConfig` implements `GateConfig`. (`e2e/coverage_gate.go:16`)
- The store port has `CarryOver` and `PutCarryOver`, and `ConfigService` and the server API have no method and no route for them. (`pkg/gate/store.go:154-170`, `pkg/store/internal/gatestore/settings.go:174`, `rg -n "signature-carry-over" internal pkg e2e --glob '!*_test.go'` gives no match) This change does not add them. See "Parked: the carry-over setting".

## What Changes

- NEW routes at the collection and the unit scope: list, get, set and delete of approval rules, and get, set and reset of the approval default. They use `gateScopeOf`, the actions `namespace:read-gates` and `namespace:manage-gates`, and the same fresh authentication, If-Match and error rules as the namespace rows.
- NEW service support for the installation scope in `gate.ConfigService`: read, save and delete of gates, access limits and approval rules, with the zero fence and the audit scope `@server`. The service still refuses an approval default at the installation.
- NEW read routes at the installation scope: GET of the list and GET of one object for gates, access limits and approval rules. They declare `installation:read-gates`.
- NEW action `installation:manage-gates` (owner, 2026-11-20), in the operator family. The installation operator and the site administrator hold it.
- NEW write routes at the installation scope: PUT and DELETE of one gate, access limit or approval rule. They declare `installation:manage-gates`, need a fresh authentication and write audit entries with the actions `installation:set-gate`, `installation:delete-gate`, `installation:set-access-limit`, `installation:delete-access-limit`, `installation:set-approval-rule` and `installation:delete-approval-rule`.
- Docs: the rows of the new routes in `docs/reference/gate-config-api.md` and `docs/guides/approval-rules.md`, and the row of the new action in the sensitive-action table of `docs/guides/principals-and-groups.md`.
- ADR records: none to change. The "Changed by" item for ADR 299 in ADR 230, the reverse item in ADR 299 and the changelog entries are on main (task 3.4 verifies them).
- Regenerated API description and clients, and the e2e rows.

No route for the signature carry-over setting (owner, 2026-11-20). ADR 299 decision 12 (owner) deletes the setting and replaces it with the binding level. The rows of ADR 230 decisions 2 and 3 that name the setting are not built. The binding level gets its own routes in the review-request change.

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

Decided by the owner on 20 November 2026. This change records them as facts. No question is open.

- **Q1 (owner, 2026-11-20): `installation:manage-gates` is in the operator family.** The installation operator and the site administrator hold it. The reasons are in the ADRs: `installation:read-gates` is in the operator family (`pkg/access/access.go:128`), and ADR 216 decision 2 says "An action has operator scope when it changes how the whole installation runs, or when it reads or writes data of more than one namespace." An installation gate, access limit or approval rule acts on every namespace. The audit action names of the installation writes follow the names of main: `installation:<verb>-<kind>`, as `namespace:<verb>-<kind>` is for the namespace (`pkg/gate/config_objects.go:15-18`, `pkg/gate/config.go:114-117`).
- **Q2 (owner, 2026-11-20): build no signature carry-over route.** ADR 299 decision 12 replaces the setting. ADR 230 has the "Changed by" item for ADR 299, with the reverse item in ADR 299 and a changelog entry in each (on main; task 3.4 verifies it). The words of every owner decision stay as they are.
- **ADR 216 decision 1 (owner, 18 Oct 2026):** "Operator scope and namespace scope (owner)." Q1 follows it.
- **ADR 299 decision 12 (owner, 1 Nov 2026):** "The binding level takes over signature carry-over (owner. Details by the PM)." Q2 follows it.
- **ADR 335 decision 5 (owner, 7 Nov 2026):** "A save needs a flag when locks are held". This change does not build it (see "Out of scope").

## ADR check

Each quote is copied from the file in `docs/adr/`. Owner tags and "Changed by" chains are from the files and from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (owner-tagged: decision 1; decision 3 is marked "(PM)").

- Decisions that apply: 1, 2, 3 (amended by ADR 268 and ADR 355), 4 (extended by ADR 335 and ADR 355), 5 and 6.
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4; ADR 299 changes decisions 1, 2, 3 and 7 (on main since commit 3e4e918; task 3.4 verifies it). Chain to the end: ADR 268, ADR 286, ADR 299, ADR 335, ADR 355. ADR 299 has later items from ADR 328, 359 and 368. None of them touches decision 12.
- Current rule: this ADR, with ADR 268, 286, 299, 335, 355.
- Quote (decision 1): "In the server, through the API (owner). Gates, access limits, approval rules, approval defaults, ownership rules, path sets and the signature carry-over setting are stored in the server."
- Quote (decision 2): "`installation:manage-gates` | the installation | gates, access limits, approval rules, ownership rules, path sets and the signature carry-over setting of the installation".
- Quote (decision 2): "`installation:read-gates` | the installation | read the gates, limits, approval rules, ownership rules and path sets of the installation".
- Quote (decision 3): "`GET`, `PUT .../approval-default` | read, manage" (the row for `{scope}`) and "`GET .../gates`, `GET .../gates/{id}`, and the same for `.../access-limits`, `.../approval-rules`, `.../ownership-rules` and `.../path-sets` | `installation:read-gates`".
- Quote (decision 3): "`PUT`, `DELETE .../gates/{id}`, and the same for `.../access-limits/{id}`, `.../approval-rules/{id}`, `.../ownership-rules/{id}` and `.../path-sets/{id}` | `installation:manage-gates`".
- Quote (decision 3): "A list route gives the objects stored at that scope, not the objects of wider scopes. A `PUT` names no scope in its body, and refuses an unknown field."
- Quote (decision 6): "A scope holds at most 256 gates, 64 access limits, 64 approval rules, 256 ownership rules and 64 path sets. A condition holds at most 4 KiB."
- Effect: The change serves the missing rows of decision 3 for approval rules and approval defaults, and the installation read rows. The installation write rows follow owner decision Q1 (owner, 2026-11-20): the action `installation:manage-gates` is in the operator family. The carry-over rows are not built (owner, 2026-11-20, Q2). Decision 2 asks for a fresh authentication for the change of an approval rule, an approval default, an access limit and an enforce gate. Main already asks for it for every write of these objects, and the change keeps main. The ADR sets a minimum, so this is not a conflict.
- Conflict: ADR 230 decisions 2 and 3 name the signature carry-over setting. ADR 299 decision 12 (owner) deletes it, and ADR 230 has the "Changed by" item for ADR 299 on main. The owner decided on 20 November 2026 that this change builds no carry-over row, and task 3.4 verifies the item. The words of ADR 230 stay as they are.

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
- Effect: Installation gates act on every namespace, so the rule puts `installation:manage-gates` in the operator family (owner, 2026-11-20). The list of installation actions in this ADR has no gate action. The owner decision fills that gap, as the family of `installation:read-gates` shows. The roles `installation-operator` and `site-administrator` hold the operator family (ADR 216 context: "The role `installation-operator` holds the operator family. `site-administrator` holds all families.").
- Conflict: none.

**ADR 299: Review requests and change orders: one integration with the Git host** (owner-tagged: decision 12).

- Decisions that apply: 12.
- Changed by: ADR 328 extends decision 1; ADR 359 extends decisions 1, 15 and 25; ADR 368 changes decision 19. Chain to the end: ADR 328, ADR 359, ADR 368. None of them touches decision 12.
- Current rule: this ADR, with ADR 328, 359, 368.
- Quote: "The binding level takes over signature carry-over (owner. Details by the PM)."
- Quote: "The setting `signatureCarryOver` has no users and no stored data. It is deleted with no conversion."
- Quote (consequences): "The code of `signatureCarryOver` is deleted in its own pull request."
- Effect: Carry-over routes would serve a setting that this decision deletes. The change builds none (owner, 2026-11-20).
- Conflict: with ADR 225 decision 12 and ADR 230 decisions 2 and 3 for the carry-over rows. The "Changed by" chain of ADR 225 lists ADR 299 decision 12 as a change, so ADR 225 is already changed. ADR 230 is marked on main.

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

none

Open questions: none. The two questions of the first draft are answered by the owner (2026-11-20). Q1: `installation:manage-gates` is in the operator family. Q2: no carry-over route. See "Owner decisions that apply".

Questions considered and answered:

- Who holds `installation:manage-gates`? The operator family: the installation operator and the site administrator (owner, 2026-11-20).
- Do the routes of the signature carry-over setting get built? No (owner, 2026-11-20). ADR 299 decision 12 replaces the setting.
- Which audit action names do the installation writes use? `installation:<verb>-<kind>`, the form of the namespace names on main (`pkg/gate/config_objects.go:15-18`). The owner delegated the names to this change with Q1.
- Are gate objects stored in the server? Yes. Decision 1 of ADR 230 and ADR 222 decision 6.
- Is the DELETE route of the approval default in scope for the collection and the unit scope? Yes. ADR 268 gives it for each scope, and it answers 204 also when none is set.
- Which key names the unit scope of gate rows? The stable unit ID, by the owner decision of 19 October 2026 in issue 1003. The path keeps the by-name form of ADR 355. Main does this already, and the conformance suite tests it. This change adds only tests that the new routes go through `gateScopeOf`.
- Does a PUT without If-Match answer 428? No on main. The earlier draft of this change said so, and no ADR asks for it. The reference page says that a PUT without If-Match replaces the stored object. This change keeps that rule.
- Does a route answer 403 with code gate.denied for a caller without the action? No. Main answers access.denied. The code gate.denied is for the refusal of a change by a gate.
- Is a new reference page needed? No. `docs/reference/gate-config-api.md` and `docs/guides/approval-rules.md` exist. The change adds rows to them.
- Does MinApprovals need removal work? No. `rg -n -F --glob '!*_test*' 'MinApprovals' pkg internal cmd` gives 0 matches.
- Do the installation read routes need the new action? No. `installation:read-gates` exists and is classified.
