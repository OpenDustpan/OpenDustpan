---
version: 2
---

Roadmap: `docs/roadmap.md:360` (Blueprints).

## Why

**Persona: developer.** As a Developer, I try to create a stack or workspace from a blueprint without special knowledge, and I hit unclear input errors and silent quota refusals.

The user path this change must make work:

1. **Docs page.** Publish a blueprint version for each tool from the guide
2. **API.** Submit a Self-service request with answers through the tool-neutral API
3. **API.** Read the named-input error or the quota error and fix the document
4. **CLI.** Watch the runner job apply the new stack or workspace

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Request names an input that the blueprint does not allow | 400 self-service.bad-input: input `size` is not allowed; read the blueprint version and its input schema. | `GET /api/dustpan/v1/self-service/blueprints/{namespace}` |
| Quota refuses a request past its limit | 409 self-service.quota-exceeded: quota `units` for blueprint `postgres` is exceeded; list the units of the namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Request names a blueprint of another namespace or a missing name | 404 self-service.blueprint-not-found: blueprint is unknown in this namespace; list the blueprints of the namespace. | `GET /api/dustpan/v1/self-service/blueprints/{namespace}` |

Evidence for the claims above:

- A publish guide exists. Proof: docs/guides/publish-a-blueprint.md
- No e2e test in the pack names Blueprint. Proof: Blueprint e2e: [] with e2e_count 0
- Runner e2e and code exist for the job part of the step. Proof: e2e/jobs_plan_test.go and pkg/runner
- Blueprint routes for read, draft, versions and retire exist on main. Proof: route GET /api/dustpan/v1/self-service/blueprints/{}

## What Changes

- Publish blueprint versions per tool in a namespace through the existing route POST /api/dustpan/v1/self-service/blueprints/{}/{}/versions
- Add the new package pkg/catalog if it is not yet in the checkout; it holds the request, the blueprint and the choice engine
- NEW route POST /api/dustpan/v1/namespaces/{namespace}/self-service/requests for request submit with Idempotency-Key
- NEW route GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests for request list
- NEW route GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request} for request read
- Submit validates inputs and names the bad input
- Provision one stack or workspace in its collection and start one runner job
- Enforce units and requests quota rules with reserve, settle and release

Out of scope:

- Ephemeral units with time to live and destroy approvals
- Environment promotion and comparison of configs
- Artifact registry, curated upstreams and upgrade pull requests
- Job proxy rules and credential handling
- Console catalog and create form
- Gates for blueprint rules, owned by turn-blueprint-rules-catalog-into

Depends on:

- add-sql-adapter-quota-counter
- `add-sql-adapter-quota-counter` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The quota error shows the rule, the key and the one list command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Blueprint read refuses hostile rows (`pkg/store/postgres/self_service_blueprints_internal_test.go:93 (TestSelfServiceBlueprintReadRefusesHostileRows)`)
- Request body-invalid error code exists (`internal/errcode/catalog_request.go:7 (request.body-invalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 110: The Catalog: a gateway of declarative requests on the job model** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current), 6 (current), 7 (current), 8 (current).
- Changed by: ADR 210 changes; ADR 218 extends; ADR 222 supersedes decision 9; ADR 225 changes decision 9; ADR 286 changes; ADR 305 extends; ADR 308 extends. Chain to the end: ADR 210, ADR 218, ADR 222, ADR 225, ADR 286, ADR 305, ADR 308, ADR 247, ADR 248, ADR 335, ADR 351, ADR 354, ADR 299, ADR 360, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 210, 218, 222, 225, 286, 305, 308.
- Quote: "One request, one unit. A Catalog request asks for one unit from one blueprint, in the namespace of the request."
- Quote: "A request has revisions. Each submit of a document makes a new revision of the request, with a number that grows by 1."
- Quote: "The Catalog makes the unit before its first job. A job needs a unit with a stable unit ID (ADR 100 decision 2)."
- Quote: "The Catalog starts jobs through `job.Service` only. A revision gives one `job.StartRequest`."
- Quote: "Render is an API call. `catalog.Renderer` is a port with one adapter for each tool, in the internal wiring of the server."
- Effect: The change follows one request one unit, revisions, provision before the job, jobs through job.Service, and render through a tool adapter.
- Conflict: none.

**ADR 115: Quotas and reservations** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 5 (current), 6 (current), 9 (current), 10 (current).
- Changed by: ADR 222 changes decision 2; ADR 222 supersedes decision 13; ADR 224 changes decision 2; ADR 224 supersedes decision 13; ADR 226 supersedes decision 13; ADR 241 extends; ADR 286 changes. Chain to the end: ADR 222, ADR 224, ADR 226, ADR 241, ADR 286, ADR 335, ADR 351, ADR 354, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 222, 224, 226, 241, 286.
- Quote: "A tool-neutral package `pkg/quota`. It holds the rules, the usage, the reservations and the port `quota.Counter`."
- Quote: "A reservation holds a unit for a create in flight."
- Quote: "A reservation ends in one of two ways."
- Quote: "The settled count follows the inventory."
- Effect: The change reserves in the submit transaction, settles or releases at revision end, and rechecks at approval and apply.
- Conflict: none.

**ADR 218: What a namespace is for, and when to make a namespace or a collection (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 248 changes decisions 1, 2 and 5; ADR 286 changes. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248, 286.
- Quote: "A blueprint never serves another namespace (owner). A request of the catalog (ADR 110) in namespace A can use only the blueprints of A."
- Effect: A request uses only blueprints of its own namespace.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "Quotas. A quota reservation (`units` and `requests` rules of ADR 115) stays a fixed control."
- Effect: Units and requests rules stay fixed controls; expr rules are another item.
- Conflict: none.

**ADR 286: Self-service, failover and promotion: three renames** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Other uses of "catalog" stay (PM decision under the owner's delegation)."
- Effect: Copy says Self-service; the package name follows ADR 110.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does one request make one or many units? ADR 110 decision 2: one unit. Decisions 6 and 7: provision first, then one job.
- How do quotas count units in flight? ADR 115 decisions 4 to 6 and 9: settled plus reserved, settle or release, inventory wins.
- Can a request use a blueprint of another namespace? ADR 218 decision 3: no, answered as not found.
- Which language holds the input schema? JSON Schema on the blueprint version; ADR 110 decision 8 names none.
- Where do quota rechecks run? ADR 115 decision 10 and ADR 226 decision 11.
- How does a retry stay idempotent? ADR 110 decision 7: key is request ID and revision; the submit also uses Idempotency-Key.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
