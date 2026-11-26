---
version: 8
---

Issue: #929.

## Why

**Persona: namespace administrator.** A namespace administrator tries to decide which stacks and workspaces can read the outputs of a producer. Main stores the namespace setting for new units and serves it on `GET` and `PUT` of the output-sharing route. The setting value `namespace` has no effect on a new unit, and a purge of a unit or a collection leaves no record of the rules that name it. The administrator needs a new unit that gets its first rule from the setting, so that no one edits each producer by hand, and needs a purged object to take its rules with it.

The user path this change makes possible:

1. **Store.** Set the setting to `namespace` (on main), make a unit, and the unit has one rule that names its namespace. With `nobody` it has none.
2. **Store.** Purge a unit or delete a collection, and each rule that names it goes in the same transaction, with the removed rules in the audit entry.

The routes that save, list and remove a rule, the e2e steps and the docs page are in the change `add-output-sharing-rules-api`, which depends on this change. A person reaches the new-unit rule through the existing unit routes and the existing audit log, and reads the rule through the routes of that change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A unit is made with a setting that fails validation. | The create fails and nothing is written. The answer names the namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/output-sharing` |

Evidence for the claims above:

- Issue 929 asks for the namespace setting, rules at namespace and collection level, the read check across units and audit entries. Proof: #929 Scope: In: the namespace setting; sharing rules at namespace and collection level; the read check for outputs across units; audit entries.
- The owner decided the nobody default and one-step rules. Proof: #929 Decided: 15.6 (owner, 2026-10-18): output sharing of a new unit is a namespace setting, default nobody. A namespace or collection rule shares outputs in one step.
- The stores make a stack and a workspace and write no rule. Proof: `internal/store/sqlite/sqlite.go:638` and `internal/store/sqlite/workspaces.go:31` make the unit and call no rule write.

## What Changes

- The stack and workspace stores of both dialects write the first rule in the transaction that makes the unit, when the setting of the namespace is `namespace`.
- The purge of a unit and the delete of a collection remove each rule that names the object by stable ID, in the same transaction, and list the removed rules in the audit entry.
- The conformance cases for both, with a body, and the hostile table of the store.

Out of scope:

- The rule service, the routes, the API description, the e2e steps and the docs page. The change `add-output-sharing-rules-api` owns them.
- The read check of a job token against the stored rules. The change `output-sharing` owns it.
- Sharing between namespaces as a new feature, console pages and a move of the setting into the generic settings API.

Depends on:

- none

Builds on (not a change):

- #897 (the generic settings API). The setting does not use it.

Is depended on by:

- add-output-sharing-rules-api
- output-sharing

## Usability

- The setting changes only the units that the namespace makes later. The docs page of `add-output-sharing-rules-api` says so next to the setting.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The setting has a port, `outputshare.Settings`, with `Setting` and `SetSetting` that append the audit entry in the transaction. (`pkg/outputshare/ports.go:106`)
- `outputshare.Store` declares Put, Get, List, Delete and Find for the rules. The SQL adapters implement them since part 1. (`pkg/outputshare/ports.go:41`, `internal/store/sqlite/outputsharerules.go:34`, `internal/store/postgres/outputsharerules.go`)
- The types `Rule`, `Default`, `ID`, `Placement` and their validation exist. (`pkg/outputshare/outputshare.go:181`, `:208`, `:239`)
- The setting column and its CHECK exist in both baselines, and the backup holds the column. (`internal/store/sqlite/migrations/0168_baseline.sql:23`, `internal/store/postgres/migrations/0168_baseline.sql:799`, `pkg/backup/portable_tables.go:92`)
- The table `output_sharing_rules` keeps the rules since part 1. (`internal/store/sqlite/internal/db/queries.sql.go:1631`)
- The service `Setting` and `SetSetting` check `namespace:read` and `unit:manage-sharing` and write the audit entry of the change. (`internal/outputsharesvc/outputsharesvc.go:74`, `:96`)
- The routes `GET` and `PUT` `/api/dustpan/v1/namespaces/{namespace}/output-sharing` exist, with the body `{"newUnitDefault":"nobody"}` or `"namespace"`. (`internal/api/inventory/outputsharing.go:49`, `internal/api/inventory/routes.go:143`)
- The action `unit:manage-sharing` has the object kinds unit, collection and namespace, and the built-in `admin` role holds it. (`pkg/identity/identity.go:226`) The actions `unit:read-access`, `collection:read-access` and `namespace:read-access` have a unit, a collection and a namespace object, and the access-review routes use them in that order. (`pkg/identity/identity.go:356`, `:353`, `:350`, `internal/api/server/review.go:307`, `:308`, `:309`)
- The stores make a stack and a workspace in one fenced transaction. They write no rule. (`internal/store/sqlite/sqlite.go:638`, `:650`, `internal/store/sqlite/workspaces.go:31`, `:43`, `internal/store/postgres/postgres.go:435`, `:444`, `internal/store/postgres/workspaces.go:34`, `:43`)
- The conformance suite names 37 rule cases, 2 setting-and-audit cases and 5 new-unit cases with no body. They skip until an adapter runs them. The SQL harness gives `Store: nil` and `Unit: nil`. (`pkg/outputshare/outputsharetest/outputsharetest.go:201`, `:250`, `internal/store/sqlite/outputshare_test.go:51`, `:65`)
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
- Quote (decision 10): "The routes of a rule on a collection and on a namespace have the same shape under `collections/{collection}` and under the namespace." The owner decided the reading of "the same shape" on 2026-11-20 (owner, 2026-11-20): the list of a collection needs `collection:read-access` on the collection and the list of a namespace needs `namespace:read-access` on the namespace, as the list of a unit needs `unit:read-access` on the unit. Decision 10 names no action for these two lists, so this owner decision fills the gap. It is recorded in the ADR check of this change until an ADR amendment carries it.
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026: stable IDs** (owner decision 1, 2026-11-08).

- Decisions that apply: 1 (current).
- Changed by: none in `docs/adr/INDEX.md`. Current rule: this ADR.
- Quote (decision 1): "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only."
- Effect: A rule of this change stores its producer and its consumer by stable ID, in any namespace. It meets decision 1.
- Conflict: none. ADR 391 (owner, 2026-11-20) settles the consumer of another namespace.

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

Open questions: none. The owner decided on 2026-11-20 (ADR 391): "Stable ID always. No entities should be keyed by name." The rule store keeps a consumer of another namespace as that namespace plus a unit or collection ID that the caller gives, checks the format only, and removes the rule when the purge or the delete of that unit or collection runs. ADR 273 decision 3 stays for the rest: the producer decides alone, and the save does not tell whether an object of another namespace exists.

Questions considered and answered:

- What is the default for a new unit? ADR 273 decision 4: nobody. Main stores and serves it already.
- Does a default change rewrite existing units? No. ADR 273 decision 4 and the contract of `outputshare.Settings.SetSetting` (`pkg/outputshare/ports.go:106`).
- Does a collection that is made again with the old name get an old rule? No, in any namespace: the rule names its stable ID (ADR 391).
- How does the save learn the stable ID of a consumer in another namespace? The caller gives it, and the save checks the format only (ADR 391, "Not decided here"). The body of the route and the way a person finds the ID are decided in the change `add-output-sharing-rules-api`.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs, and rewritten against main in November 2026. Every quote, path and ADR citation was checked by code.
