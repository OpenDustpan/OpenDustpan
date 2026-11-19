---
version: 7
---

Issue: #929.

## Why

**Persona: namespace administrator.** A namespace administrator tries to decide which stacks and workspaces can read the outputs of a producer. Main stores the namespace setting for new units and serves it on `GET` and `PUT` of the output-sharing route. Main stores no sharing rule. The setting value `namespace` has no effect, and no route saves, lists or removes a rule. The administrator needs rules on a unit, a collection and a namespace, and a new unit that gets its first rule from the setting, so that no one edits each producer by hand.

The user path this change must make work:

1. **API.** Read the setting with `GET /api/dustpan/v1/namespaces/{namespace}/output-sharing` and set it to `nobody` or `namespace` with `PUT` (already on main). Make a unit and list its rules: with `namespace` it has one rule that names its namespace, with `nobody` it has none.
2. **API.** Save, list and remove a rule on a unit, on a collection and on a namespace, by stable ID.
3. **E2e.** Run the rule steps against the real server and the real CLIs. The steps that read outputs with a job token wait for the change `output-sharing` (see "Depends on").
4. **Docs page.** Read the output sharing guide for the rule routes and the audit entry of each rule change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A rule names a principal, a whole other namespace, an unknown unit, an unknown collection, or a stable ID that belongs to another object. | 400 for an invalid rule and 404 for an unknown object. The answer names the target and stores no rule. | `GET /api/dustpan/v1/namespaces/{namespace}/collections` |
| A rule with the same producer and the same consumer exists. | 409. The answer names the rule ID. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/sharing-rules` (NEW route) |
| The producer already has 100 rules. | 409 with the limit in the text. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/sharing-rules` (NEW route) |
| The caller lacks `unit:manage-sharing` on the object of the route. | 403 that names the action. | `GET /api/dustpan/v1/namespaces/{namespace}/output-sharing` |

Evidence for the claims above:

- Issue 929 asks for the namespace setting, rules at namespace and collection level, the read check across units and audit entries. Sharing between namespaces is not in its scope. Proof: #929 Scope: In: the namespace setting; sharing rules at namespace and collection level; the read check for outputs across units; audit entries. Out: sharing between namespaces.
- The owner decided the nobody default and one-step rules. Proof: #929 Decided: 15.6 (owner, 2026-10-18): output sharing of a new unit is a namespace setting, default nobody. A namespace or collection rule shares outputs in one step.
- The setting route exists and the output-sharing route holds the setting only. Proof: `internal/api/inventory/outputsharing.go:49` serves GET and PUT of the setting and no rule. `rg -n "sharing-rules" --glob '!*_test.go' --glob '!openspec/**' --glob '!docs/**' .` finds only `e2e/output_sharing_test.go` on main.
- The audit said that the existing output-sharing route carries rule CRUD, and that the setting may move to the generic settings API. Both are wrong on main. The rule routes are separate paths (ADR 273 decision 10), and the setting is served on its own route with its own port (`pkg/outputshare/ports.go:106`).

## What Changes

- The rule store: one table in the baseline of each dialect, the SQL adapters of `outputshare.Store` (Put, Get, List, Delete, Find), and the removal of the rules of a purged unit.
- The rule of a new unit: the stack and workspace stores write the first rule in the transaction that makes the unit, when the setting of the namespace is `namespace`.
- The rule service and the NEW routes of the rules, under `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/sharing-rules`, `/api/dustpan/v1/namespaces/{namespace}/collections/{collection}/sharing-rules` and `/api/dustpan/v1/namespaces/{namespace}/sharing-rules`: three rows for a unit and the same shape for a collection and for a namespace (the table is in design.md). The setting route is not moved or changed.
- The audit entry of each rule write, in the transaction of the write.
- The docs page update.

Out of scope:

- The read check of a job token against the stored rules, the check of the starter grants and of the last saver of a trigger, and the denial audit of a read by a job. The change `output-sharing` owns them, and it reads the rules that this change stores.
- Sharing between namespaces as a new feature: the store keeps and matches a consumer of another namespace as ADR 273 decision 3 says, and the routes accept it.
- Console pages for sharing.
- A move of the setting into the generic settings API (decision 2 of the design).
- Listing the rules of a collection or a namespace: parked, see "Open questions".

Depends on:

- none

Builds on (not a change):

- #897 (the generic settings API). The setting does not use it.

Is depended on by:

- output-sharing

## Usability

Where the path still costs the person time or surprises them:

- An administrator may expect a rule to follow a collection name. The docs say that a rule follows the stable ID of a collection of its own namespace, and that a rule that names a collection of another namespace follows the name, as ADR 273 decision 3 says.
- The setting changes only the units that the namespace makes later. The docs say so next to the setting.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The setting has a port, `outputshare.Settings`, with `Setting` and `SetSetting` that append the audit entry in the transaction. (`pkg/outputshare/ports.go:106`)
- `outputshare.Store` declares Put, Get, List, Delete and Find for the rules. No adapter implements them: the SQL adapters return `outputshare.Settings` only. (`pkg/outputshare/ports.go:41`, `pkg/store/sqlite/outputshare.go:23`, `pkg/store/postgres/outputshare.go:24`)
- The types `Rule`, `Default`, `ID`, `Placement` and their validation exist. (`pkg/outputshare/outputshare.go:181`, `:208`, `:239`)
- The setting column and its CHECK exist in both baselines, and the backup holds the column. (`pkg/store/sqlite/migrations/0168_baseline.sql:23`, `pkg/store/postgres/migrations/0168_baseline.sql:799`, `pkg/backup/portable_tables.go:92`)
- No table keeps a sharing rule. `rg -n -i "sharing_rule|output_sharing_rule" pkg/store/*/migrations` finds nothing.
- The service `Setting` and `SetSetting` check `namespace:read` and `unit:manage-sharing` and write the audit entry of the change. (`internal/outputsharesvc/outputsharesvc.go:74`, `:96`)
- The routes `GET` and `PUT` `/api/dustpan/v1/namespaces/{namespace}/output-sharing` exist, with the body `{"newUnitDefault":"nobody"}` or `"namespace"`. (`internal/api/inventory/outputsharing.go:49`, `internal/api/inventory/routes.go:143`)
- The action `unit:manage-sharing` has the object kinds unit, collection and namespace, and the built-in `admin` role holds it. (`pkg/identity/identity.go:219`)
- The stores make a stack and a workspace in one fenced transaction. They write no rule. (`pkg/store/sqlite/sqlite.go:638`, `:650`, `pkg/store/sqlite/workspaces.go:31`, `:43`, `pkg/store/postgres/postgres.go:435`, `:444`, `pkg/store/postgres/workspaces.go:34`, `:43`)
- The conformance suite names 37 rule cases, 2 setting-and-audit cases and 5 new-unit cases with no body. They skip until an adapter runs them. The SQL harness gives `Store: nil` and `Unit: nil`. (`pkg/outputshare/outputsharetest/outputsharetest.go:201`, `:250`, `pkg/store/sqlite/outputshare_test.go:51`, `:65`)
- The checker denies a read by a job token and says that no path serves it. (`internal/outputsharesvc/checker.go:226`)
- The guide says that the server does not keep sharing rules yet. (`docs/guides/output-sharing.md:20`)
- The e2e file has the helpers `sharingRule` and `checkSharingRuleRefusals`, and skips the two job steps with `sharingRulesSkip`. (`e2e/output_sharing_test.go:41`, `:600`, `:623`, `:783`, `:809`)

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or when the ADR records decisions of the project owner.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (the status line says it records decisions 15.1 to 15.7 of the owner and the product manager; decision 4 is the owner decision 15.6, option c).

- Decisions that apply: 2, 3, 4, 10, 11, 12 (all current).
- Changed by: ADR 286 changes (renames only). Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote (decision 4): "A namespace setting decides the rule of a new unit, and the default is "nobody" (15.6, option c)."
- Quote (decision 4): "With `namespace`, the store writes one rule on a new unit that names the namespace of the unit. The SQL adapter of the store writes the unit, the rule and the audit entry of the rule in the one transaction that makes the unit."
- Quote (decision 4): "A change of the setting changes no rule of a unit that exists."
- Quote (decision 10): "`GET /namespaces/{namespace}/output-sharing` | `namespace:read` on the namespace" and "`PUT /namespaces/{namespace}/output-sharing` | `unit:manage-sharing` on the namespace".
- Quote (decision 10): "`POST /namespaces/{namespace}/units/{unit_id}/sharing-rules` | `unit:manage-sharing` on the unit" and "`DELETE /namespaces/{namespace}/units/{unit_id}/sharing-rules/{rule_id}` | `unit:manage-sharing` on the unit".
- Quote (decision 10): "The routes of a rule on a collection and on a namespace have the same shape under `collections/{collection}` and under the namespace."
- Quote (decision 3): "A rule can name a unit or a collection of another namespace as its consumer. It never names a whole other namespace. It never names a principal of any namespace."
- Quote (decision 11): "The rule of a new unit is a write inside the SQL adapters (decision 4), not a port."
- Quote (decision 12): "The store runs `Rule.Validate` at each write."
- Effect: Decision 10 answers the open question of the audit. The setting stays on the output-sharing route, and each rule has its own route under `sharing-rules`. This change adds the store, the service, the routes and the seeding that decisions 4, 10, 11 and 12 describe.
- Conflict: none. The list route of a collection and of a namespace has no decided action (see "Open questions").

**ADR 322: No migrations or compatibility code before the first tag** (owner decisions 1, 2, 4 and 5).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place. It adds no numbered migration."
- Effect: The rule table goes into the baseline of each dialect in place, in name order, with no migration.
- Conflict: none.

**ADR 250: Defense in depth for security code** (the project owner decided it).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 382 extends decision 3. Current rule: this ADR, with ADR 382.
- Quote: "Never only one layer. Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Effect: The hostile-case tables of the design have one row for each layer: the HTTP adapter, the service, and the store with its table.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current). Changed by: ADR 315 extends.
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it."
- Effect: The guide update ships in the same pull request.
- Conflict: none.

## Open questions

Open questions: one, parked. No task depends on it:

- Parked: the action that lists the rules of a collection and of a namespace. ADR 273 decision 10 gives `unit:read-access` for the list on a unit, and says the routes of a collection and of a namespace have "the same shape". `unit:read-access` has a unit object only (`pkg/identity/identity.go:355`), and `collection:read-access` has a collection object and `namespace:read-access` a namespace object (`pkg/identity/identity.go:349`, `:352`). No ADR says which of them lists rules. Who may read the rules of a collection or a namespace is a security choice, so the change does not decide it. The save and the removal on a collection and on a namespace use `unit:manage-sharing`, which its definition allows for both objects (`pkg/identity/identity.go:219`). The person reads a saved rule from the answer of the save.

Questions considered and answered:

- What is the default for a new unit? ADR 273 decision 4: nobody. Main stores and serves it already.
- Does a default change rewrite existing units? No. ADR 273 decision 4 and the contract of `outputshare.Settings.SetSetting` (`pkg/outputshare/ports.go:106`).
- Do the rules use the output-sharing route or a new path? ADR 273 decision 10: separate paths under `sharing-rules`. The output-sharing route keeps the setting.
- Does the setting move to the settings API of #897? No. The ADR names the output-sharing route, main serves it with its own port and audit entry, and the generic API has no per-feature key until a feature adds it (`openspec/changes/add-one-generic-settings-api/proposal.md`, "Out of scope"). A move is a new decision and needs an RFC.
- Which action covers the save and the removal? `unit:manage-sharing` on the object of the route (ADR 273 decision 10). The change adds no action.
- Does a collection that is made again with the old name get an old rule? In the namespace of the producer, no: the rule names its stable ID. A collection of another namespace is kept by name, and a new one with the old name matches, as ADR 273 decision 3 says.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs, and rewritten against main in November 2026. Every quote, path and ADR citation was checked by code.
