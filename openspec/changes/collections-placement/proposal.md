Roadmap: `docs/roadmap.md:195` (Collections and placement).

## Why

**Persona: namespace administrator.** A namespace administrator shares one namespace across teams and must keep each new stack or workspace in the right collection. A CLI create needs one explicit collection, so that grants on collections stay exact. This change places each CLI-made unit by a stored placement rule or the TFE project. It refuses the create, and makes no unit, when no collection is found.

The user path this change must make work:

1. **Docs page.** Read the collections and placement page for rule order and the TFE project mapping
2. **API.** Create a collection with the existing collection create route, then list the collections and the placement rules of the namespace
3. **API.** Create, order or delete placement rules for a Pulumi project and a stack-name pattern
4. **CLI.** Run `pulumi stack init` and `tofu init` with a cloud block project, and see the unit in its collection or a refusal that names the fix
5. **E2e.** Make a namespace, delete its catch-all rule if it has one, make a collection through the API, then run the placement steps against the real binary and the real CLIs and check the audit entries

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| pulumi stack init with no matching placement rule | 422: no placement rule of namespace acme matches Pulumi project shop and stack dev. Add a placement rule, or make the unit through the API with a collection. | `GET /api/dustpan/v1/namespaces/acme/placement-rules` |
| tofu init with a missing or unknown TFE project | 422: workspace web of namespace acme names no existing collection. Set `project` in the `cloud` block to a collection, make the collection, or make the workspace through the API with a collection. | `GET /api/dustpan/v1/namespaces/acme/collections` |
| The caller has no grant to make a unit in the chosen collection | 403: the caller cannot make a unit in collection platform, chosen by placement rule 2. | `GET /api/dustpan/v1/namespaces/acme/placement-rules/2` |

Evidence for the claims above:

- Placement rules already have e2e files and a guide, so this change completes them Proof: keyword evidence for Placement rule lists e2e/placement_test.go, e2e/unit_without_collection_test.go and docs/guides/collections-and-placement.md
- No e2e file in the pack names the TFE project path Proof: keyword evidence for TFE project: e2e_count 0, empty e2e list, empty guides, empty code
- The placement rule routes and the collection routes exist and have no console view Proof: coverage_gaps: createPlacementRule, listPlacementRules, getPlacementRule, updatePlacementRule, deletePlacementRule, createCollection, listCollections, console cell no

## What Changes

- Ordered placement rules map a Pulumi project and stack name to one collection. The first match wins.
- The project of the cloud block maps a new workspace to one collection.
- A CLI create with no found collection fails, makes no unit and no collection, writes an audit entry and names the fix.
- Each placement, each refusal and each save, reorder or delete of a rule writes an audit entry in the same transaction as the change it records.
- A rule that names a missing collection is refused when it is saved.
- The rule list returns the rules in match order.
- A rule is data for new units only. A rule change never moves an existing unit.
- The e2e steps use the existing collection create route (createCollection) to make collections, and make a namespace with no catch-all rule so that the refusal is tested.

Out of scope:

- The default collection, the catch-all placement rule of a new namespace and the opt-out of the namespace create request (a namespace-create item)
- Collection move, delete and new create APIs beyond placement use
- The grant model and the planner role
- Blueprint collection choice
- Console views

Depends on:

- groups-roles-grants
- `groups-roles-grants` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused stack init costs the developer a round trip to the administrator, so the error names the namespace, the project and the stack.
- Two overlapping rules surprise the team when a broad early rule wins, so the rule list shows the order.

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
- Effect: This change refuses a unit with no collection and keeps membership explicit. The owner decision (queue P5) makes a stored, visible, audited default collection and a catch-all rule at namespace create. Both are explicit stored data that a person can delete, so no match, no unit holds. This change adds no implicit default, reads the catch-all as an ordinary rule and changes no decision words.
- Conflict: none.

**ADR 61: A unit from the CLI gets its collection from placement rules or the TFE project** (not owner-tagged).

- Decisions that apply: 1 (extended), 2 (current), 3 (extended), 4 (current), 6 (current).
- Changed by: ADR 329 extends decisions 1 and 3. Chain to the end: ADR 329.
- Current rule: this ADR, with ADR 329.
- Quote: "When the Pulumi CLI makes a stack, the server takes the collection of the first rule that matches."
- Quote: "When OpenTofu makes a workspace, the `project` of its `cloud` block names the collection."
- Quote: "The server does not make a collection on its own (ADR 51)."
- Quote: "After the server finds the collection, it checks that the caller has a grant that allows it to make a unit in that collection, on the collection or on an object that holds it (ADR 52)."
- Quote: "Each change passes the policy check and writes an audit entry."
- Effect: The change implements rules, the TFE project, the refusal, the grant check and the audit entry of each rule change.
- Conflict: none.

**ADR 346: A unit moves between collections, never between namespaces** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A person can move a unit to another collection of the same namespace. The authorization model must allow it (ADR 51 decision 6)."
- Effect: A move is an explicit API call. A rule change never moves a unit. This change adds no move.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The change ignores owner decision P5 (13 Nov 2026): a new namespace gets a stored default collection and a catch-all placement rule. proposal.md says Open questions none and does not list P5 as in scope or out of scope, so the refusal scenarios in spec.md cannot be built right without it. Resolution: answered: The owner decision for the default collection and catch-all rule is cited in considered and adr_check. Its creation is out of scope and the rule is treated as an ordinary stored rule.
- R1.2 (adr conflict): ADR 51 decision 4 says 'No default collection exists', and P5 adds a default collection at namespace create. The ADR check in proposal.md says Conflict: none, with no proof. An RFC is needed before the change can be built, since the conflict touches ADR 51, which is not owner-tagged. Resolution: answered: The owner decision for the default collection and catch-all rule is cited in considered. The namespace-create item owns it, so this change adds no default collection and no conflict arises here.
- R1.3 (missing scenario): spec.md has no scenario for namespace create with the default collection and catch-all rule, the opt-out, or deleting the default collection or the catch-all rule. The catch-all rule would change the no-match refusal in spec.md. Resolution: answered: The default collection scenarios belong to the namespace-create item and are listed in out_of_scope. The no-match scenarios here also cover an empty rule list.
- R1.4 (scope): The default collection and catch-all rule belong to a namespace-create item, not to this placement item. tasks.md does not say whether this change takes that work or excludes it. Resolution: answered: out_of_scope now excludes the default collection, the catch-all rule and the opt-out.
- R1.5 (adr conflict): ADR 61 decision 4 requires the make-unit grant check after the collection is found. spec.md does not say whether the check runs before the refusal of a missing collection, so a caller without a grant can learn that a collection does not exist. Resolution: answered: considered says that the grant check comes first for callers with no right. A spec requirement and a scenario cover the grant refusal.
- R1.6 (failure mode): Concurrent creates into the same name or collection, and a collection deleted between rule evaluation and the unit create, are not covered. The resolver's refusal can race with the commit. Resolution: answered: A transaction with the fence is in design and considered. A scenario covers a collection deleted in flight.
- R1.7 (missing scenario): No scenario covers a rule that names a collection the caller cannot use, a rule that names a deleted collection, or when rules are validated (at save or at create). Resolution: answered: Scenarios cover a rule naming a missing collection, and rules are validated at save.
- R1.8 (open question): design.md says the resolver validates order, patterns and targets once, but does not say what happens when a rule is reordered or a target collection is removed while a create is in flight. Resolution: answered: The resolver reads rules and collections in the create transaction. Rules are checked at save. A scenario covers a collection deleted in flight.
- R1.9 (missing scenario): No scenario covers hostile pattern input in a placement rule (very long or regex-like patterns) or the resolver with an empty rule list. Resolution: answered: A pattern limit scenario and an empty rule list case were added.
- R1.10 (failure mode): The stated risk that an unmatched TFE project blocks every workspace create has no mitigation and no scenario for the audit record of the refusal, although project.md requires a record for each automatic action. Resolution: answered: A requirement and a scenario cover the audit entry of a refusal. The risk names the fix in the error.
- R1.11 (wrong claim): The evidence line says placement has e2e coverage for Pulumi paths, citing e2e files whose content is not in the input. The claim is unchecked. Resolution: answered: The evidence claims now cite only the file paths that the pack gives.
- R1.12 (wrong claim): The TFE refusal scenario does not say that a person can make the collection or make the unit through the API with a collection, which ADR 61 decision 3 requires. The spec also does not require the help command text that the proposal's error table names. Resolution: answered: The TFE refusal scenario now names the three ways to fix it and the help command.
- R2.1 (owner decision conflict): proposal.md ADR check and Open questions treat owner decision P5 (new namespace gets a stored default collection and catch-all rule) as out of scope, yet the roadmap and ADR 51 decision 4 ('No default collection exists') conflict with it; the change does not record an RFC or an owner decision that reconciles the default collection with ADR 51 decision 4, so the ADR conflict is left unresolved and the 'Conflict: none' claim is unsupported. Resolution: answered: The owner decision keeps the rule explicit and stored, so ADR 51 decisions 4 and 5 hold. The ADR check and considered say so. The default is made by the namespace-create item.
- R2.2 (adr conflict): ADR 51 decision 4 says 'Each unit is in exactly one collection. No default collection exists.' The change's refusal scenarios assume a rule list with a catch-all from P5 can exist, but the proposal states 'Conflict: none' without quoting a reconciling decision. Under the ADR rule an RFC is needed first. Resolution: answered: The ADR 51 effect now says the stored catch-all is an explicit rule, so no match, no unit holds and no RFC is needed. No decision words change.
- R2.3 (missing scenario): spec.md has no scenario for a unit moved between collections by a rule change, nor for rule reordering while a create is in flight beyond a single delete case; ADR 346 decision 2 (owner) allows moves and the change does not say whether rule edits affect existing units. Resolution: answered: Requirement Existing units stay says a rule change never moves a unit. ADR 346 is in the ADR check.
- R2.4 (missing scenario): No scenario covers a caller who can make units but cannot read the chosen collection, nor the order of checks when the caller has no grant and the collection is missing (the 403 vs 422 ordering is stated in prose only, not in a requirement). Resolution: answered: Requirement Order of checks and two scenarios fix the 403 before 422 order and the no-read case.
- R2.5 (failure mode): Concurrent creates of the same unit name are listed as a risk in design.md (one unit and one conflict error) but no spec scenario or task test covers it, and the tasks do not map it to a failing test. Resolution: answered: Requirement Same-name creates and a failing test task cover it.
- R2.6 (failure mode): The pattern-limit scenario does not state the limit value or the behavior for non-ASCII or control characters in a glob; a hostile pattern can pass the length check and still cause expensive matching across many rules. Resolution: answered: The pattern limits are 128 bytes, printable ASCII and at most 4 wildcards, with a limit of 100 rules.
- R2.7 (open question): Behavior when a placement rule's target collection is deleted after save is not specified: rules become dangling, and the spec covers only save-time validation and in-flight deletion, not later deletes that leave stored rules pointing at a missing ID. Resolution: answered: Requirement Rule that names a deleted collection covers a stale rule.
- R2.8 (wrong claim): proposal.md claims 'Placement rules already have e2e files and a guide in the pack' citing e2e/placement_test.go and docs/guides/collections-and-placement.md, but the input does not include these files; the claim is unchecked per the evidence rule. Resolution: answered: The claim now cites the keyword evidence list of the pack, which names the files and the guide.
- R2.9 (scope): tasks.md section 3 omits a task to update the placement rule routes' console state or to record the audit for rule save and reorder, although ADR 61 decision 6 requires each placement rule change to write an audit entry; the spec only requires an audit entry for refusals. Resolution: answered: Requirement Record of a rule change and a task for the audit entries cover it.
- R2.10 (failure mode): Audit requirement covers only refusals; successful placements are not audited in the spec, though design decision 3 says each placement writes an audit entry. Resolution: answered: Requirement Record of a placement covers successful placements.
- R3.1 (owner decision conflict): proposal.md treats owner decision P5 (new namespace gets a stored default collection and catch-all rule) as out of scope, yet the spec's no-match scenario (spec.md 'Refuse stack with no match') assumes an empty or non-catching rule list; the change does not state how a catch-all created by P5 interacts with the no-match refusal and the 'Order of checks' requirement, so the spec cannot be built right without the answer. Resolution: answered: considered now says the catch-all is an ordinary rule in list order. No-match applies only when it is deleted. Check order is unchanged. Scenarios cover both cases.
- R3.2 (adr conflict): ADR 51 decision 4 ('No default collection exists') is quoted in proposal.md ADR check as having 'Conflict: none', but P5 adds a default collection at namespace create; the change relies on the owner decision to reconcile without an RFC, and the ADR rule requires an RFC when a proposal contradicts an accepted ADR. Resolution: answered: The owner decision P5 settles it: the default is explicit and stored, so ADR 51 d4 and d5 hold and no RFC is needed. Cited in adr_check and considered.
- R3.3 (missing scenario): No scenario covers a rule save while a stack-name pattern matches a collection the administrator cannot read, or a rule that names a collection the caller can use but that another caller cannot read (visibility of the rule list order to restricted users). Resolution: answered: Scenario Collection not readable in Rule list order covers it.
- R3.4 (failure mode): The 'Same-name creates' requirement covers two Pulumi CLI processes only; it does not cover a Pulumi create racing a TFE workspace create that targets the same unit name, nor the audit entry of the losing create. Resolution: answered: Scenario Pulumi and TFE race and Conflict is audited cover it.
- R3.5 (failure mode): The 'Record of a placement' and 'Record of a refusal' requirements do not say whether the audit write is in the same transaction as the unit insert; if the audit write fails after the unit is stored, the placement is unaudited, which breaks the 'nothing happens without a record' rule. Resolution: answered: Record of a placement now requires the audit write in the unit transaction, with a rollback scenario.
- R3.6 (missing scenario): No scenario covers a rule list at the limit of 100 rules, nor a save that would exceed it; design.md states the limit but spec.md has no requirement or scenario for it. Resolution: answered: Requirement Rule count limit with two scenarios was added.
- R3.7 (open question): The ordering of rule reorder with a concurrent create is not specified: a create that reads rules before a reorder commits may place the unit by the old order; the spec does not say which order wins. Resolution: answered: Requirement Reorder with a create says the order read in the create transaction wins.
- R3.8 (scope): tasks.md section 2 does not include a task for the placement rule list order display required by the usability section ('the rule list shows the order'), and no spec requirement covers it. Resolution: answered: Requirement Rule list order and a task for it were added.
- R3.9 (wrong claim): The evidence line claims placement rules have e2e files and a guide in the pack, but the cited keyword evidence is not reproduced with an rg command or its output, so the negative claim 'no e2e file names the TFE project path' is unchecked under the evidence rule. Resolution: answered: The pack gives keyword evidence lists, not rg output. The claims cite those lists exactly, as the evidence rule allows.
- M.21 (gap): Owner decision P5 gives a new namespace a catch-all rule, but its namespace-create item is not in this milestone. No change names the collection create API that steps 4, 10 and 13 need. Resolution: answered: The e2e step makes the namespace without the catch-all, or deletes the rule first, so the refusal is tested. It makes collections with the existing createCollection route (POST /api/dustpan/v1/namespaces/{namespace}/collections), which the coverage list names. This change adds no collection create API.

## Open questions

none

Questions considered and answered:

- How does the catch-all rule of a new namespace interact with the no-match refusal? Owner decision, 13 Nov 2026 (queue P5): the catch-all is an ordinary stored rule. The resolver treats it like any rule, in list order, so a namespace with the catch-all never hits no-match for a stack. If an admin deletes it, the no-match refusal applies. Creating the catch-all belongs to the namespace-create item.
- How does the e2e step test the refusal if a new namespace gets a catch-all rule? The step deletes the catch-all rule through the existing placement rule delete route, or makes the namespace with the opt-out that the owner decision gives. Then it makes collections through the existing collection create route. This change adds no new route for either.
- Does a stored default collection conflict with ADR 51 decision 4, and is an RFC needed? Owner decision, 13 Nov 2026 (queue P5): the default and the rule are explicit, stored, visible and audited, so ADR 51 decisions 4 and 5 and ADR 61 decision 3 hold. No RFC is needed for this change.
- Can a unit exist with no collection? No. ADR 51 decisions 4 and 5 and ADR 61 decision 3 require refusal with nothing made.
- Do rules also place units made through the API? No. ADR 61 decision 5: rules and projects apply only to units that a CLI makes.
- What is the order of checks? The server checks that the caller can make a unit in the namespace, then finds the collection (422 if none), then checks the grant on the chosen collection (403). A caller with no right in the namespace never learns whether a collection exists.
- What if a collection is deleted during a create, or after a rule names it? The rule keeps the stable ID. The unit insert and the collection check run in one transaction with the namespace fence. A deleted collection gives the missing-collection refusal and no unit.
- What if a rule reorder runs with a create? The create reads the rules inside its transaction. The order that is committed when the create reads wins.
- Does a rule change move existing units? No. ADR 346 decision 2 gives the move as an explicit call.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
