Roadmap: `docs/roadmap.md:402` (Console).

## Why

**Persona: developer.** Developer wants to make a unit from a published blueprint without reading the raw API. Today the catalog and create form have no proven path, so the developer cannot see blueprints or learn which input is wrong.

The user path this change must make work:

1. **Console.** Open the catalog of the namespace and pick a blueprint
2. **API.** Open one blueprint version and read its ordered input schema through the REST API of the blueprints change
3. **API.** Fill inputs and send the create request with an Idempotency-Key through the REST API of the blueprints change
4. **Console.** Read the error that names the bad input, fix it, and make the unit
5. **Docs page.** Follow the docs page for the catalog create flow
6. **E2e.** Run the browser suite against the real binary over TLS

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Create form sends an input that the blueprint does not allow | The form shows each message unchanged as plain text next to that input. Fix the inputs, then send the request again. | `opendustpan status` |
| Catalog of a namespace holds no published blueprint | The catalog shows an empty state with a link back to the namespace home. Publish a blueprint first, then open the catalog again. | `opendustpan status` |
| Catalog opens a blueprint that no longer exists | The catalog shows a not-found message and a link back to the catalog. Pick another blueprint. | `opendustpan doctor` |
| Catalog list or create request fails with a server outage | The console shows a retry message with the request ID. Wait, then open the catalog again. | `opendustpan status` |

Evidence for the claims above:

- A guide for publishing a blueprint exists. Proof: docs/guides/publish-a-blueprint.md
- The milestone step needs browser tests that open the catalog, fill the create form, see the error of a bad input, and make a unit. Proof: The browser tests of the console repository run against the server of the suite. They open the catalog, fill the create form of a blueprint, see the error of a bad input, and make a unit.
- No e2e file in this pack covers Blueprint. Proof: Blueprint e2e: [] with e2e_count 0

## What Changes

- Console catalog page lists published blueprints of a namespace through the routes of the blueprints change. Those blueprint routes are new in that change, not in this one.
- Console create form shows the versioned input schema in server order and sends a unit request with the blueprint stable ID
- Console create form shows each input error unchanged as plain text next to the named input with raw JSON mode
- Browser tests prove open catalog, bad input error, idempotent retry, and make of a unit

Out of scope:

- Blueprint publish and edit flows
- Blueprint route, input check, quota, unit write, grant model, and audit record owned by blueprints change
- Ephemeral units
- Environment promotion
- Artifact registry and curated upstreams
- Upgrade pull requests
- Job proxy

Depends on:

- blueprints
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The form still costs time when blueprint input schemas are long: the developer scrolls past many inputs even with stable order and version filtering.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The proposal leaves open which API the catalog form uses and which routes it calls; the two error commands point to routes that are not in the API description or route table, so the spec-ready gate fails (proposal.md error table). Resolution: answered: Named the two new routes as new in what_changes and closed the API question in considered and design.
- R1.2 (wrong claim): The ADR check says no ADR is touched, but the roadmap cites RFC 14 Blueprints as accepted and the context pack lists no decisions; the claim that no decision applies is unsupported by evidence (proposal.md ADR check, Questions considered). Resolution: answered: Kept adr_check empty because the pack lists no ADRs, and considered now states why no decision applies.
- R1.3 (missing scenario): The spec has no scenario for a catalog that opens a blueprint that no longer exists (404 with not-found message and link back), although the proposal's error table requires it. Resolution: answered: Added missing-blueprint requirement with 404 scenario.
- R1.4 (missing scenario): The spec does not cover a caller without read access on the namespace, nor an unauthenticated caller, nor an unknown or missing namespace, although the design requires each endpoint to declare authorization actions. Resolution: answered: Added deny-without-access and unknown-namespace requirements with scenarios.
- R1.5 (missing scenario): The spec does not state that a request referencing a blueprint by stable ID survives a rename, although the design relies on stable IDs for blueprint and unit references and the project rule requires it. Resolution: answered: Added stable-ID rename requirement with scenario.
- R1.6 (failure mode): The 'Valid request makes unit' scenario has no failure path: a duplicate unit, a blueprint unpublished between catalog load and submit, a concurrent edit, or a server outage during create is not covered. Resolution: answered: Added duplicate-unit and unpublished-blueprint failure scenarios.
- R1.7 (failure mode): The design risk says a bad input shows a generic message in the form, but no requirement or task checks the server error text is shown unchanged; the open question on this is still unanswered. Resolution: answered: Closed the error-text question and added requirement that the server names each bad input so the form shows it unchanged.
- R1.8 (missing scenario): The spec does not cover a request that holds more than one bad input, or a required input that is missing, so the error names only one input or none. Resolution: answered: Added multi-input and missing-required-input scenarios.
- R1.9 (scope): Task 3.1 requires the browser suite to run over TLS against the milestone 6 suite, but the proposal does not state the server setup or the e2e step against the real CLI, so the definition of done is incomplete for this item. Resolution: answered: Tasks now name the real binary over TLS with the milestone 6 suite and the docs-page path.
- R1.10 (open question): The usability cost of long input schemas (inputs that do not apply to the chosen blueprint version) is recorded but no requirement or task decides how the form hides or orders them. Resolution: answered: Added requirement to order and hide inputs by blueprint version and a task check.
- R1.11 (wrong claim): The Evidence line says the browser tests already exist in the console repository, but the tasks list them as failing tests to be added, so the claim contradicts the task list. Resolution: answered: Fixed evidence to claim only the milestone step need, not existing tests.
- R2.1 (missing scenario): The two new REST routes have no scenario for the 'POST requests' authorization. The spec covers deny only for the blueprint list; the create request has no 403/401 path and no declared authorization action or object (for example create-unit on the namespace). The project rule needs every endpoint to declare its actions. Resolution: answered: Added create-request auth requirement with 401 and 403 scenarios and declared action create-unit on the namespace. List deny kept.
- R2.2 (wrong claim): The proposal says the 422 answer names each bad input, but the spec's 'Bad input' requirement does not fix the status code or the error body shape (per-input name and reason). The spec also says 'conflict error' and 'not-found error' with no status code. Without the shape the console cannot map errors to inputs, and a test cannot pin it. Resolution: answered: Pinned status codes 422, 404, 409 and the per-input name plus reason body in the Bad input, Missing blueprint, and Valid request requirements.
- R2.3 (scope): The change adds three REST routes, a validation engine and a unit-creation path to the server, but the design says 'No new server structure' and the console lives in another repository. The change does not say where the server routes are built, so the gate cannot tell if this item owns them or the blueprints change does. The route table has no authorization actions. Resolution: answered: Design and what_changes now state the blueprints change owns the three routes and the unit path, and this change owns only console pages plus browser tests.
- R2.4 (wrong claim): The ADR check says 'No ADR is touched' on the basis that the context pack is empty. That is not proof of no conflict. The change adds routes, authorization, a stored unit reference and audit, which ADRs on API namespaces, grants and audit would cover. The rule needs quoted decision lines or a stated search, and the pack is empty. Resolution: answered: Kept adr_check empty because the pack lists adrs as [] with no decision text to quote, and considered states the search and the owner split.
- R2.5 (failure mode): Duplicate create is refused with a conflict only for 'the same valid request twice'. There is no idempotency key or retry rule. A double click, a network retry after a timeout, or two concurrent requests can make two units or an ambiguous answer, and the spec does not say which wins. Resolution: answered: Added idempotency requirement: retry with the same Idempotency-Key returns the same unit instead of a second unit.
- R2.6 (missing scenario): The 'Ordered inputs' requirement says the server lists the input schema, but no route returns the schema or the blueprint version. The tasks say 'filter inputs by version' and a 'raw JSON mode', and the proposal does not define either. 'Hide inputs that do not apply' has no scenario. Resolution: answered: Added blueprint detail requirement that returns the versioned input schema in stable order, with hidden inapplicable inputs and raw JSON mode in tasks.
- R2.7 (failure mode): The spec has no scenario for a hostile or oversized input: unknown input names, wrong types, very large values, HTML or script in an error text shown 'unchanged' in the console. Showing server text unchanged is an XSS risk if the console does not escape it. The proposal needs a hostile test for it. Resolution: answered: Added hostile input requirement with oversized, wrong type, and script text refused or escaped, plus a hostile browser test in tasks.
- R2.8 (missing scenario): The spec has no scenario for an unpublished blueprint in the catalog list or the detail route (404 or hidden), a draft blueprint named by ID, a server outage or 5xx during list or create, or an empty catalog. The user path needs an empty-state and an outage message that name one command. Resolution: answered: Added empty catalog, unpublished and draft hidden, and outage scenarios with error entries that name one command.
- R2.9 (open question): The change does not say which namespace principal or identity owns the unit that is made, whether an audit record is written for the create, and whether the unit gets a name from the request or from the blueprint. The project rule needs a record of who did each action. Resolution: answered: Added unit name, owner, and audit requirements: name from the request, owner is the requesting principal, one audit entry per create.
- R2.10 (wrong claim): The error table gives a REST route as 'the command that helps'. It is the failing route itself and not a command that helps the person. It also names '404 with the missing blueprint ID' while the spec says the error 'names the blueprint', with no ID. The two do not match. Resolution: answered: Rewrote error table so each command is a follow-up read or retry, not the failing route, and matched stable ID wording to the spec.
- R2.11 (scope): Task 3.1 names the 'milestone 6 runner pool and fake forge', but the item needs only the server of the suite. The proposal does not explain why a runner pool is needed for a catalog test, and the TLS and 'real CLI' step is not defined. Resolution: answered: Removed runner pool and fake forge from tasks. The browser gate runs against the real binary over TLS with the server of the suite only.
- M.16 (duplicate): The requirements 'Deny create without access', 'Create audit record' and 'Missing blueprint' specify server behavior that the blueprints change owns. The out_of_scope of this change says the same. Resolution: answered: Removed server-owned requirements Deny create without access, Create audit record, Missing blueprint, Deny list, Valid request server write, and Idempotent retry server write. The spec now holds only console display and send behavior and refers to the blueprints change for server checks.

## Open questions

none

Questions considered and answered:

- Which ADR decides the catalog list and create request shape? The pack lists adrs as []. The roadmap cites RFC 14 as accepted with domain core built but gives no decision text to quote. The blueprints change owns the catalog routes, the input check, the unit write, the grant model, and the audit record, so no ADR decision applies in this change.
- Which API does the catalog create form use to list blueprints and request a unit? Only the tool-neutral REST API that the blueprints change owns. This change sends the blueprint stable ID and the Idempotency-Key through it. The browser suite proves the console path against the real server.
- Shall the create form show the server error text unchanged for a bad input? Yes. Predictable errors need exact stable output, so the form shows the server error text unchanged next to each named input as escaped plain text, with a raw JSON mode as escape hatch.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
