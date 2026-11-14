Issue: #929.

## Why

**Persona: namespace administrator.** A namespace administrator tries to decide which stacks and workspaces can read the outputs of each new stack or workspace. With no setting and no one-step rule, each producer needs unit-by-unit edits, which is slow and error prone.

The user path this change must make work:

1. **API.** Set outputSharing.newUnitDefault to nobody or namespace in namespace settings, then read the effective value.
2. **API.** Add a one-step rule on producer unit A that names unit B, its collection, or its namespace.
3. **CLI.** Run a Pulumi StackReference read and an OpenTofu remote state read from a job of unit B and see pass or deny as the rules say.
4. **Docs page.** Read the guide page for output sharing and the audit entries of rule changes and denied reads.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Job of unit B reads outputs of unit A with no naming rule or without unit:read-outputs of the starter. | 403 with error code unit.outputs-forbidden and the one command that helps: GET the producer sharing rules and the starter grants. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs` |
| Save of a namespace or collection rule names an unknown object or a unit of another namespace. | 400 with error code output-sharing.bad-target and the one command that helps: list collections of the namespace and retry with a stable ID. | `GET /api/dustpan/v1/namespaces/{namespace}/collections` |
| Two administrators save outputSharing.newUnitDefault at the same time. | 409 with error code namespace.settings-conflict and the one command that helps: GET the namespace settings and retry with the current version. | `PATCH /api/dustpan/v1/namespaces/{namespace}/settings` |

Evidence for the claims above:

- Issue 929 asks for the namespace setting, namespace and collection rules, the cross-unit read check, and audit entries, with sharing between namespaces out of scope. Proof: #929 body Scope: In: the namespace setting; sharing rules at namespace and collection level; the read check for outputs across units; audit entries. Out: sharing between namespaces.
- Issue 929 requires default nobody to deny a read from a new unit and one collection rule to allow reads within it. Proof: #929 Acceptance criteria: With default nobody, a job of unit B cannot read outputs of new unit A. A collection rule "share with collection" lets each unit in it read the others' outputs.
- Issue 929 records owner decision 15.6 of 2026-10-18 for the nobody default and one-step namespace and collection rules. Proof: #929 Decided: 15.6 (owner, 2026-10-18): output sharing of a new unit is a namespace setting, default nobody. A namespace or collection rule shares outputs in one step.
- No current code names StackReference outside tests in the searched trees. Proof: rg -n -F --glob '!*_test.go' 'StackReference' pkg internal cmd matches 0

## What Changes

- Namespace setting outputSharing.newUnitDefault with values nobody and namespace, default nobody, with effective value in the settings read
- One-step sharing rules on the producer for a unit, a collection, or a namespace by stable ID
- Read check on all output paths at each read: rule match plus unit:read-outputs of the starter, fail closed
- Audit entries for rule and default changes and for denied output reads
- Docs page for output sharing

Out of scope:

- Sharing between namespaces
- Reads of secret values beyond the read-outputs and read-secrets checks
- Console run page for sharing

Depends on:

- #897

## Usability

Where the path still costs the person time or surprises them:

- An administrator who renames a collection may expect rules to follow the name; the docs show rules follow the collection, and the API shows the stable ID.
- A job that starts allowed and reads denied after a rule removal surprises the runner; the error names the rule check and the audit entry names the remover.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 1 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "**A job reads the outputs of another unit on two paths, each with two checks.** The server checks both conditions of a path at each read."
- Effect: The read check keeps both checks of each of the two job-token paths at each read.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it."
- Effect: The change ships the user documentation page in the same pull request.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 5 (current), 7 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "A separate action reads outputs (15.1, option a). The new tool-neutral action `unit:read-outputs` allows a read of the root outputs of the latest state of a unit, and nothing else."
- Quote: "**A namespace setting decides the rule of a new unit, and the default is "nobody" (15.6, option c).** The namespace setting `outputSharing.newUnitDefault` has the values `nobody` and `namespace`."
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).** A job token reads the outputs of another unit only when both checks pass at each read:"
- Quote: "The action covers only the paths that read outputs (15.3, option a). `unit:read-outputs` covers only a route that answers outputs and nothing else:"
- Effect: This change implements the namespace default and the namespace and collection rules that these decisions require. Decision 4 carries the owner decision 15.6 of 2026-10-18 recorded in issue 929.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (missing scenario): The proposal error table and the design layer table require a 400 output-sharing.bad-target answer and a 403 for a cross-namespace target, but the spec has no scenario for rejecting an unknown or cross-namespace target. Resolution: answered: Added scenario "Invalid target rejected" in One-step rules that rejects unknown IDs and cross-namespace targets with output-sharing.bad-target.
- R1.2 (missing scenario): The read check covers only the two job-token tool paths. The tool-neutral outputs route and the TFE-compatible output routes named in ADR 273 decision 7 and in tasks 2.3 have no scenario. Resolution: answered: Added scenario "Tool-neutral and TFE output routes checked" in Read check with the same two checks on the tool-neutral outputs route and TFE-compatible output routes.
- R1.3 (open question): No action or authorization is named for creating or removing a sharing rule or for changing outputSharing.newUnitDefault. Every endpoint must declare its actions, and no ADR answers which action covers these writes. Resolution: open question: Added open question on which actions cover rule writes and the default change; no ADR answers it.
- R1.4 (open question): The spec does not say whether a change of outputSharing.newUnitDefault applies to existing units or only to units created later. Resolution: open question: Added open question on whether the default change rewrites existing units or seeds only new units.
- R1.5 (missing scenario): The design store layer and task 2.2 require that a collection rename or re-create does not widen sharing, but the spec has no scenario for it. Resolution: answered: Added scenario "Rename keeps sharing by ID" in One-step rules: rules match by stable collection ID, so rename keeps sharing and re-create with a new ID does not inherit it.
- R1.6 (missing scenario): The proposal promises audit entries for denied output reads, but the Audit requirement covers only rule and default changes. Denied reads appear only in the default-nobody scenario. Resolution: answered: Added scenario "Denied read audited" in Audit of sharing that writes an audit entry with actor, producer, consumer, and time.
- R1.7 (failure mode): The design layer test requires a deny after the starter grant is removed mid-job, but the spec has no scenario for removing the grant. Only rule removal is covered. Resolution: answered: Read check now has a grant-removed mid-job scenario: the next read is denied.
- R1.8 (failure mode): A producer unit or a collection that is deleted and re-created, or a rule that names a deleted object, is not covered. Stale rules and their read outcome are unspecified. Resolution: answered: Stale targets requirement states a rule that names a deleted unit or collection denies, and a deleted and re-created object with a new ID gets no access.
- R1.9 (missing scenario): The 409 namespace.settings-conflict answer for two simultaneous saves of outputSharing.newUnitDefault has no scenario in the spec or in the tasks. Resolution: answered: Added scenario "Concurrent saves conflict" in New-unit default with 409 namespace.settings-conflict.
- R1.10 (open question): The spec does not say what rule the namespace value seeds for a new unit: whether it names the namespace ID, and whether it covers collections. Resolution: open question: Added open question on what rule the namespace value seeds; recommended one rule that names the namespace ID.
- R1.11 (scope): The issue body says it depends on #897, but the proposal does not mention the dependency or check it. Resolution: answered: depends_on now names #897 from the issue body.
- R2.1 (open question): The spec-ready gate is not met: open question 3 (seed rule of the namespace value) is still open, and the proposal states it is not spec-ready, yet the change lists 'Open questions' with recommended answers that are not yet accepted; tasks 2.x cannot be built until the rule seed and rule-write actions are decided (openspec/changes/add-output-sharing-setting-new/proposal.md, Open questions). Resolution: open question: Gate still blocked: three open questions remain with PM recommendations; build tasks state the block and proceed only after PM decides.
- R2.2 (open question): No ADR answers which authorization action covers rule writes and the newUnitDefault change; the spec names no action and the route cannot declare its actions (proposal.md Open questions 1; project.md requires each endpoint to declare actions). Resolution: open question: Added rule-write-actions and default-change-action requirements with unit:manage-sharing and namespace:manage-settings; open question 1 kept for PM confirmation.
- R2.3 (adr conflict): ADR 273 decision 4 says the default is nobody and the namespace value is a setting; the change adds a new-unit rule seed for namespace without an ADR decision on the seeded rule shape; the proposal treats this as open, so the spec scenario 'Namespace default allows' asserts a pass without a defined rule. Resolution: answered: Namespace-default-allows scenario now states the seeded shape as one rule that names the namespace ID, matching the recommended answer of open question 3.
- R2.4 (wrong claim): The ADR check says ADR 273 is not owner-tagged while its decision 4 cites owner decision 15.6 from 2026-10-18 recorded in issue 929; the change does not show the owner decision text it relies on for default nobody. Resolution: answered: ADR check effect and evidence now cite owner decision 15.6 recorded in issue 929 and its carry in ADR 273 decision 4.
- R2.5 (missing scenario): No scenario covers a read by a job whose producer unit is in another namespace via a collection rule, or a rule whose collection is moved between namespaces; the cross-namespace guard is only at save time. Resolution: answered: Added cross-namespace read denied scenario that denies a job whose unit is in another namespace even with a collection rule.
- R2.6 (failure mode): Concurrent rule writes and a read racing a rule removal are not specified; the spec fixes only the next read, not the in-flight read, and the store fence scenario is absent from the spec. Resolution: answered: Added in-flight read fails closed scenario and concurrent saves conflict scenario with fence in the same transaction.
- R2.7 (missing scenario): The spec has no scenario for a rule that names a unit whose secret outputs are read via check 2 with unit:read-secrets; the read check requires only unit:read-outputs, while the proposal lists secret reads as out of scope without a scenario. Resolution: answered: Added secret output scenario that requires unit:read-secrets on the producer in addition to unit:read-outputs.
- R2.8 (scope): Task 2.2 and 2.3 build the audit of denied reads and the sharing rule store; the proposal's dependency on #897 is recorded but the tasks do not check that #897 is merged before the read check is wired. Resolution: answered: Build task now checks that #897 is merged before the read check is wired.
- R2.9 (failure mode): A trigger-started job reads outputs under ADR 267 decision 14 with the last saver's grants; the change does not say which principal check 2 uses for a trigger start, so a saver who is deleted or disabled is not covered. Resolution: answered: Added trigger saver scenario that uses the last saver grants of ADR 267 decision 14 and denies when the saver is missing or disabled.

## Open questions

1. Which authorization actions cover rule writes and the default change? Decides: PM.
   - Options: New action unit:manage-sharing on the producer for rule writes, namespace:manage-settings on the namespace for the default; Reuse unit:manage on the producer and namespace:manage-settings on the namespace.
   - Recommended: New action unit:manage-sharing on the producer for rule writes, namespace:manage-settings on the namespace for the default Reason: Every route must declare its actions, and least privilege favors a narrow sharing action over broad manage.
2. Does a change of outputSharing.newUnitDefault change existing units? Decides: PM.
   - Options: Only units made after the change; All existing units without an explicit rule plus new units.
   - Recommended: Only units made after the change Reason: A default that rewrites existing sharing widens access without an explicit rule write, which breaks least surprise.
3. What rule does the namespace value seed on a new unit? Decides: PM.
   - Options: Seed one rule that names the namespace ID; Seed one rule per collection of the namespace.
   - Recommended: Seed one rule that names the namespace ID Reason: One rule that names the namespace ID gives namespace-wide reads in one step with the fewest stored rules.

Not spec-ready: question 3: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001.

Questions considered and answered:

- What is the default sharing of a new unit? ADR 273 decision 4 answers it: the namespace setting outputSharing.newUnitDefault with values nobody and namespace, default nobody, which carries owner decision 15.6 recorded in issue 929.
- How does one step share outputs with a namespace or a collection? ADR 273 decisions 1 and 5 answer it: a rule on the producer that names the unit, its collection, or its namespace, plus unit:read-outputs of the starter.
- When does a changed rule take effect for a running job? ADR 213 decision 1 and ADR 273 decision 5 answer it: both checks at each read, so a later grant or rule change applies at the next read.
- Which principal does check 2 use for a trigger-started job? ADR 267 decision 14 answers the trigger part: a trigger start runs as the namespace service principal and check 2 uses the current grants of the trigger last saver; a missing or disabled saver means deny.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
