---
version: 2
---

Roadmap: `docs/roadmap.md:195` (Collections and placement).

## Why

**Persona: namespace administrator.** A namespace administrator shares one namespace across teams and must keep each new stack or workspace in the right collection, so that grants on collections stay exact. This change places each CLI-made unit by a stored placement rule or the TFE project. It refuses the create, and makes no unit, when no collection is found. A new namespace works the first time because its create transaction stores a visible default collection and a catch-all rule. The create request can opt out.

The user path this change must make work:

1. **Docs page.** Read the collections and placement page for rule order, the default collection and the TFE project mapping
2. **API.** Make a namespace through the existing namespace create route. It gets a default collection and a catch-all rule unless the request sets the opt-out.
3. **API.** Make a collection, then list the collections and the placement rules of the namespace
4. **API.** Make, update, order or delete placement rules
5. **CLI.** Run `pulumi stack init` and `tofu init` with a cloud block project and see the unit in its collection, or a refusal that names the fix
6. **E2e.** Run the placement steps against the real binary and the real CLIs, on a namespace with the default and on a namespace with the opt-out, then check the audit entries

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| pulumi stack init with no matching placement rule | 422 placement.no-match: no placement rule of namespace acme matches Pulumi project shop and stack dev. Add a placement rule, or make the unit through the API with a collection. | `GET /api/dustpan/v1/namespaces/acme/placement-rules` |
| tofu init with a missing or unknown TFE project and no matching rule | 422: workspace web of namespace acme names no existing collection. Set `project` in the `cloud` block, make the collection, or make the workspace through the API with a collection. | `GET /api/dustpan/v1/namespaces/acme/collections` |
| The caller has no grant to make a unit in the chosen collection | 403: the caller cannot make a unit in the collection that the placement rule chose. | `GET /api/dustpan/v1/namespaces/acme/placement-rules/{rule}` |

Evidence for the claims above:

- Placement rules already have e2e files and a guide, so this change completes them Proof: keyword evidence for Placement rule lists e2e/placement_test.go, e2e/unit_without_collection_test.go and docs/guides/collections-and-placement.md
- No e2e file in the pack names the TFE project path Proof: keyword evidence for TFE project: e2e_count 0, empty e2e list, empty guides, empty code
- The placement rule routes and the collection routes exist and have no console view Proof: coverage_gaps: createPlacementRule, listPlacementRules, getPlacementRule, updatePlacementRule, deletePlacementRule, createCollection, listCollections, console cell no

## What Changes

- Ordered placement rules map a Pulumi project and stack name to one collection. The first match wins.
- Placement rules also match OpenTofu workspace names. A named project that exists wins; the rules apply when no project is set.
- The project of the cloud block maps a new workspace to one collection.
- The existing namespace create request gets a new optional opt-out field. Without it, the create transaction stores a collection named default, a catch-all rule that names it by stable ID, and an audit entry for each. If any part fails, nothing is stored.
- A CLI create with no found collection fails with 422, makes no unit and no collection, and names the fix.
- Each placement, each refusal and each save, reorder or delete of a rule writes an audit entry. Placement and rule-change entries are in the same transaction as the change.
- A rule that names a collection that does not exist in the same namespace is refused when it is saved.
- The rule list returns the rules in match order.
- A rule is data for new units only. A rule change never moves an existing unit.

Out of scope:

- Collection move and delete APIs
- The grant model and the planner role
- Blueprint collection choice
- Console views
- Namespaces made before this change keep their data; no backfill of a default

Depends on:

- groups-roles-grants
- `groups-roles-grants` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused stack init costs the developer a round trip, so the error names the namespace, the project and the stack.
- Two overlapping rules surprise a team when a broad early rule wins, so the list shows the order.
- A new namespace works the first time because of the default. An administrator who wants strict placement uses the opt-out or deletes the catch-all rule.
- The pattern grammar and limits are provisional and the docs page says so.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Error codes placement.no-match, placement.conflict, placement.invalid-pattern, placement.rule-not-found and placement.too-many-rules exist (`internal/errcode/catalog_placement.go:7 (func catalog entries)`)
- Error codes collection.not-found and collection.already-exists exist (`internal/errcode/catalog_collection.go:7 (func catalog entries)`)
- TestPlacementRuleDoesNotAuthorize exists (`pkg/access/access_test.go:841 (func TestPlacementRuleDoesNotAuthorize)`)
- TestReviewOfACollection exists (`pkg/access/review_collection_test.go:65 (func TestReviewOfACollection)`)
- TestApplyStepsRefuseHostilePulumiNames exists (`pkg/dustpan/jobservice_applysteps_internal_test.go:120 (func TestApplyStepsRefuseHostilePulumiNames)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 51: Collections: a named set of units in a namespace** (not owner-tagged).

- Decisions that apply: 4 (current), 5 (extended), 6 (extended).
- Changed by: ADR 61 extends decision 5; ADR 248 changes decision 2; ADR 311 extends; ADR 346 extends decision 6. Chain to the end: ADR 61, ADR 248, ADR 311, ADR 346, ADR 329, ADR 286.
- Current rule: this ADR, with ADR 61, 248, 311, 346.
- Quote: "Each unit is in exactly one collection. No default collection exists. People make the collections that they need."
- Quote: "No collection, no unit. When the server cannot find the collection for a new unit, it refuses to make the unit."
- Quote: "Membership is explicit. A person or a client names the collection of a unit when it makes the unit, or moves the unit to another collection with an API call."
- Effect: The change refuses a unit with no collection and keeps membership explicit. Owner decision, 13 Nov 2026 (queue P5): the default collection and the catch-all rule are stored, visible, audited data that an administrator can delete, so no match, no unit holds. The resolver makes no implicit default.
- Conflict: none.

**ADR 61: A unit from the CLI gets its collection from placement rules or the TFE project** (not owner-tagged).

- Decisions that apply: 1 (extended), 2 (current), 3 (extended), 4 (current), 6 (current).
- Changed by: ADR 329 extends decisions 1 and 3. Chain to the end: ADR 329.
- Current rule: this ADR, with ADR 329.
- Quote: "When the Pulumi CLI makes a stack, the server takes the collection of the first rule that matches."
- Quote: "When OpenTofu makes a workspace, the `project` of its `cloud` block names the collection."
- Quote: "The server does not make a collection on its own (ADR 51)."
- Quote: "Each change passes the policy check and writes an audit entry."
- Effect: The change implements rules, the TFE project, the refusal, the grant check and the audit of each rule change. The namespace create request, not the resolver, makes the default.
- Conflict: none.

**ADR 329: Placement rules match OpenTofu unit names** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "ADR 329 extends decisions 1 and 3: placement rules also match OpenTofu unit names, and a unit that no rule matches is refused."
- Effect: Placement rules also match OpenTofu workspace names, and a unit that no rule matches is refused. The change follows this link text of the ADR.
- Conflict: none.

**ADR 346: A unit moves between collections, never between namespaces** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A person can move a unit to another collection of the same namespace. The authorization model must allow it (ADR 51 decision 6)."
- Effect: A move is an explicit API call. A rule change never moves a unit. This change adds no move.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: ADR 329: the quote "ADR 329 extends decisions 1 and 3: placement rules also match OpenTofu unit name" is not in the ADR file (copy it character for character from the decision text of the pack).

Questions considered and answered:

- Who owns the default collection, the catch-all rule and the create opt-out? This change. Owner decision, 13 Nov 2026 (queue P5): a new namespace gets a stored, visible, audited default collection and a catch-all rule in the create transaction, an admin can delete either, and the create request can opt out.
- Does a stored default conflict with ADR 51 decision 4 or ADR 61 decision 3? No. Owner decision, 13 Nov 2026 (queue P5): the rule is explicit and stored, so no match, no unit holds.
- Can a unit exist with no collection? No. ADR 51 decision 5 and ADR 61 decision 3 require a refusal with nothing made.
- Do rules place units made through the API? No. ADR 61 decision 5: rules and projects apply only to units that a CLI makes.
- What is the order of checks? The server checks the right to make a unit in the namespace (403), then finds the collection (422), then checks the grant on the chosen collection (403).
- What happens when a refusal audit write fails? The server still answers 422, because no unit exists, and logs the failed audit write at error level.
- Does a rule change move existing units? No. ADR 346 decision 2 gives the move as an explicit call.
- What are the pattern grammar and the limits? Provisional values, marked so in the docs page: the wildcard is the asterisk only, the match is case sensitive, a pattern is at most 128 bytes of printable ASCII with at most 4 wildcards, and a namespace holds at most 100 rules.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
