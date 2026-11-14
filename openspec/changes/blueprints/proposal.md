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
| Request names an input that the blueprint does not allow | 400 self-service.bad-input: input `size` is not allowed; check `GET blueprint` for inputs. | `curl /api/dustpan/v1/namespaces/{namespace}/blueprints/{blueprint}` |
| Quota refuses a request past its limit | 409 self-service.quota-exceeded: quota `units` for blueprint `postgres` is exceeded; delete a unit or wait. | `curl /api/dustpan/v1/namespaces/{namespace}/units?blueprint={blueprint}` |
| Request names a blueprint of another namespace or a missing name | 404 self-service.blueprint-not-found: blueprint is unknown in this namespace; publish it first. | `curl /api/dustpan/v1/namespaces/{namespace}/blueprints` |

Evidence for the claims above:

- A publish guide exists. Proof: docs/guides/publish-a-blueprint.md
- No e2e test in the pack names Blueprint. Proof: Blueprint e2e: [] with e2e_count 0
- Runner e2e and code exist for the job part of the step. Proof: e2e/jobs_plan_test.go and pkg/runner

## What Changes

- Publish blueprint versions per tool in a namespace
- Submit Self-service requests with input validation that names the bad input
- Provision one stack or workspace in its collection and start one runner job
- Enforce units, requests and expr quota rules with reserve, settle and release
- New route POST /api/dustpan/v1/namespaces/{namespace}/blueprints for publish
- New route GET /api/dustpan/v1/namespaces/{namespace}/blueprints for list
- New route GET /api/dustpan/v1/namespaces/{namespace}/blueprints/{blueprint} for blueprint read
- New route POST /api/dustpan/v1/namespaces/{namespace}/self-service/requests for request submit with Idempotency-Key
- New route GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests for request list
- New route GET /api/dustpan/v1/namespaces/{namespace}/self-service/requests/{request} for request read
- Use GET /api/dustpan/v1/namespaces/{namespace}/units?blueprint={blueprint} to check usage

Out of scope:

- Ephemeral units with time to live and destroy approvals
- Environment promotion and comparison of configs
- Artifact registry, curated upstreams and upgrade pull requests
- Job proxy rules and credential handling
- Console catalog and create form
- Evaluation of blueprint rules through gates at decision points, owned by turn-blueprint-rules-catalog-into

Depends on:

- add-sql-adapter-quota-counter
- `add-sql-adapter-quota-counter` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Two blueprints that differ only by tool confuse the picker; the request shows the tool label Pulumi or OpenTofu first.
- Quota refusal surprises without the current usage; the error shows the rule, the key, and the one list command.

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
- Effect: The change follows one request one unit, at most one revision in flight, provision of the unit before the job, start of jobs only through job.Service, and render through a tool adapter.
- Conflict: none.

**ADR 115: Quotas and reservations** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (changed), 4 (current), 5 (current), 6 (current), 9 (current), 10 (current).
- Changed by: ADR 222 changes decision 2; ADR 222 supersedes decision 13; ADR 224 changes decision 2; ADR 224 supersedes decision 13; ADR 226 supersedes decision 13; ADR 241 extends; ADR 286 changes. Chain to the end: ADR 222, ADR 224, ADR 226, ADR 241, ADR 286, ADR 335, ADR 351, ADR 354, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 222, 224, 226, 241, 286.
- Quote: "A tool-neutral package `pkg/quota`. It holds the rules, the usage, the reservations and the port `quota.Counter`."
- Quote: "Three kinds of rule. A blueprint version holds 0 to 16 rules (`quota.Rule`):"
- Quote: "A reservation holds a unit for a create in flight."
- Quote: "A reservation ends in one of two ways."
- Quote: "The settled count follows the inventory."
- Quote: "Recheck of quotas. At `approval` and at `apply` (ADR 113 decision 8), the check runs the `expr` rules again,"
- Effect: The change follows the tool-neutral quota package, the three rule kinds, settled plus reserved usage, reservation in the submit transaction, settle or release at revision end, hourly reconcile from inventory, and recheck at approval and apply.
- Conflict: none.

**ADR 218: What a namespace is for, and when to make a namespace or a collection (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 248 changes decisions 1, 2 and 5; ADR 286 changes. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248, 286.
- Quote: "A blueprint never serves another namespace (owner). A request of the catalog (ADR 110) in namespace A can use only the blueprints of A."
- Effect: Blueprints stay inside one namespace. A request uses only blueprints of its own namespace.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 1, 2, 4).

- Decisions that apply: 1 (current), 2 (current), 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Five parts (owner). One model answers the three questions:"
- Quote: "Six decision points (owner). `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2)."
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches, quota reservations and quota counts,"
- Effect: Quota counts stay fixed controls; gate evaluation of blueprint rules lives in turn-blueprint-rules-catalog-into.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 2).

- Decisions that apply: 2 (extended), 3 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Six decision points (owner)."
- Quote: "The recheck at `change.apply`. `change.apply` evaluates the gates again with the facts of that moment."
- Effect: Quota and gate rechecks run at change.propose and change.apply; a recheck only adds a refusal or a requirement.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "Quotas. A quota reservation (`units` and `requests` rules of ADR 115) stays a fixed control."
- Effect: Units and requests quota rules stay fixed controls with quota fact; expr quota rules run as gates at change.propose.
- Conflict: none.

**ADR 286: Self-service, failover and promotion: three renames** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Other uses of "catalog" stay (PM decision under the owner's delegation). "Catalog" stays a common noun for a fixed list:"
- Effect: The change uses Self-service as the feature name for the former Catalog and keeps catalog only as a common noun. The core package is pkg/selfservice as the renamed pkg/catalog of ADR 110 decision 1.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The input schema language is undecided (proposal.md Open questions item 1 leaves it to PM), yet specs and layers depend on JSON Schema validation at the API boundary; the change cannot be built without this answer. Resolution: answered: Fixed the schema language as JSON Schema on the blueprint version in design and specs; open_questions is now empty and considered records the answer.
- R1.2 (open question): The proposal states the spec is not spec-ready: three routes named in the user path and tasks (GET blueprint, POST blueprints, POST self-service/requests) are not in the API description or route table, so the spec-ready gate refuses implementation. Resolution: answered: Named the three routes as new in what_changes so errors and tasks reference declared new routes.
- R1.3 (adr conflict): ADR 110 decision 1 says the domain package is pkg/catalog, while the design puts the core in pkg/selfservice; the name change is ADR 286 but design decision 1 cites no ADR, so the package choice is unsupported and tasks 2.1 build against an undecided package. Resolution: answered: Design decision 1 now cites ADR 110 decision 1 as renamed by ADR 286 and the ADR 286 adr_check effect names pkg/selfservice.
- R1.4 (failure mode): Concurrent submits of the same request and the reservation idempotency key (request ID and revision) are not specified; ADR 110 decision 4 allows at most one revision in flight, but no scenario covers a second submit while one is in flight, or a retry after a crash between reserve and unit make. Resolution: answered: Added requirement Concurrent submit with scenarios for second submit in flight and retry idempotency.
- R1.5 (missing scenario): No scenario covers a blueprint of another namespace or a missing blueprint name (404 self-service.blueprint-not-found), although ADR 218 decision 3 (owner) requires not found and the proposal's error table lists it. Resolution: answered: Added scenario Other namespace refused to requirement Publish blueprint covering the 404 rule.
- R1.6 (missing scenario): No requirement covers the authorization denial at submit or publish (a token without the request action must get deny and an audit entry), though the layers table requires it as a hostile test. Resolution: answered: Added requirement Deny without action with hostile submit scenario and audit entry.
- R1.7 (failure mode): Render safety is not specified in the spec: no scenario covers shell metacharacters or template markers in inputs, nor the claim that the render gives no secret value; the layers table names this hostile test but no requirement enforces it. Resolution: answered: Added requirement Safe render with hostile metacharacter scenario and no secret value.
- R1.8 (failure mode): Quota release on failure is not covered: a failed create that wrote a state version keeps its unit (ADR 115 decision 6) and a restart or expiry during a reservation (ADR 115 decision 7) has no scenario. Resolution: answered: Added requirement Quota settle and release covering failed create that wrote state and expiry of reservation.
- R1.9 (wrong claim): The ADR check says ADR 115 decisions 4, 5 and 6 answer how quotas count, but the design's reconcile claim (hourly, inventory wins) and the 'quota count drift' risk are not linked to ADR 115 decision 9; the risk section adds an unlisted policy. Resolution: answered: Linked reconcile and drift risk to ADR 115 decision 9 in adr_check and design risks.
- R1.10 (missing scenario): The spec has no requirement for the rendered-file preview before submit, which the usability section promises, and no scenario for a blueprint revision pinning an old schema version after a new publish. Resolution: answered: Removed the rendered-file preview promise from usability and added scenario Pinned revision keeps old schema.
- R1.11 (scope): The tasks list the console and the Self-service docs page work, while the proposal's out-of-scope list excludes the console catalog and create form; task 3.3 and the console step in the user path are not reconciled with that exclusion. Resolution: answered: Removed the console step from user_path; console catalog stays in out_of_scope and no console task remains.
- R2.1 (scope): The change is not spec-ready. Its own Open questions section says a code check fails, because the route POST /api/dustpan/v1/namespaces/demo/self-service/requests is not in the API description or the route table. The text also says 'Questions considered and answered' while this failure is still open. The gate refuses it until the route is declared or replaced by an existing route. Resolution: answered: All new routes are named in what_changes with {namespace} placeholders and all task checks use the same placeholders, so no task check names an undeclared route.
- R2.2 (missing scenario): The spec has no requirement for the quota check at approval and at apply (ADR 115 decision 10). The design says it rechecks the expr rules, but no scenario covers a recheck that fails and releases the reservation. Also missing are an expr rule at change.propose as a gate (ADR 226 decision 11) and the requests rule at submit. Resolution: answered: Added requirement Quota recheck at approval and apply with failing recheck and release scenario, expr gate at change.propose scenario, and requests rule at submit scenario.
- R2.3 (missing scenario): No requirement covers the gates at change.propose, change.planned and change.apply. A deny gate, an approval wait or a freeze on the unit does not appear in the specs or tasks. The ADR check cites ADR 222 and says blueprint checks run inside the gate model, but no scenario or task shows it. ADR 224 decision 2 puts the Catalog revision before the plan at change.propose. Resolution: answered: Removed Gates at decision points per M.2; gate evaluation now owned by turn-blueprint-rules-catalog-into and listed in out_of_scope.
- R2.4 (missing scenario): The spec has no scenario for failure after the unit is made. Examples: the job fails to start, the provisioner makes the unit but the job start fails, or the name of the unit already exists (a collision). ADR 110 decision 6 says the make is idempotent by request ID, but the retry scenario only covers a crash between reserve and unit make. Resolution: answered: Added requirement Provision then start failure with unit-made but job-start-failed scenario and name-collision 409 scenario; retry keeps idempotency by request ID and revision.
- R2.5 (failure mode): The submit does not say what happens when a unit is made but the same request is renamed or removed and made again. Concurrent submits by different callers with the same blueprint and quota key are not covered. Two submits at the last free slot must give one success and one 409. There is no concurrency test for the quota reservation in the tasks. Resolution: answered: Added concurrent last-slot scenario under Concurrent submit: two submits at the last free slot give one success and one 409; added quota concurrency test to tasks.
- R2.6 (wrong claim): The ADR 115 check says decisions 4, 5 and 6 answer the quota count, and the section lists decision 2 as 'changed'. The input shows ADR 222 and ADR 224 change decision 2 (the expr rules), but the change does not read their current rule for it. The design says the idempotency key is 'request ID and revision' for the reservation. ADR 110 decision 7 gives that key for the job start, not for the reservation. Resolution: answered: Fixed design to say the job start idempotency key is per ADR 110 decision 7 and the reservation uses the same key plus quota key; adr_check notes the changed reading of expr rules under ADR 222 and ADR 224.
- R2.7 (open question): The request body shape (document, desired state present or absent, blueprint version choice) is not defined. The change says nothing about how the request names the blueprint version, nor how an update revision or a destroy is handled (ADR 110 decision 3). It also does not say whether the submit accepts only create in this step. Resolution: answered: Defined the request document in design and specs: blueprint name, pinned blueprint version, answers, desired present or absent, collection; this step ships create and update to present.
- R2.8 (missing scenario): The publish requirement has no scenario for an invalid schema in the blueprint, more than 16 quota rules, a quota window outside 1 minute to 31 days, or a republish of an existing version. Authorization for publish is in the layers table but has no scenario. The spec also lacks the lower-limit rule (ADR 115 decision 11). Resolution: answered: Added publish validation scenarios for bad schema, too many rules, bad window, republish, publish authz, and lower-limit keeps units.
- R2.9 (scope): The blueprint requirements list routes for publish, read and submit, but no list route and no request read route. The error table sends the user to GET blueprints and GET units?blueprint=, and the user path says 'watch the runner job', yet these routes are not named in What Changes. The task 1.3 render test is not mapped to any spec scenario for deterministic bytes. Resolution: answered: Named list blueprints, get request, and list units by blueprint in what_changes; render determinism test maps to Safe render and Request unit scenarios.
- R2.10 (failure mode): The design does not cover the restart or crash cases for the hourly reconcile and for the reservation expiry at deadline (ADR 115 decision 7). The spec only has a 'Rejected revision releases' scenario that lists expired, with no test of the deadline or of a restart of the server. Resolution: answered: Added requirement Reservation deadline and reconcile restart with deadline-expiry release and restart-reconcile scenarios.
- M.1 (dependency): The change enforces quota rules with reserve, settle and release, but it does not depend on add-sql-adapter-quota-counter. That change builds the SQL counter for SQLite and PostgreSQL. Resolution: answered: Added depends_on add-sql-adapter-quota-counter and design now uses its quota.Tx adapter with no second counter.
- M.2 (duplicate): The requirement 'Gates at decision points' specifies the same work as turn-blueprint-rules-catalog-into, which evaluates blueprint rules through gates at change.propose. Resolution: answered: Removed requirement Gates at decision points; gate evaluation moved to out_of_scope owned by turn-blueprint-rules-catalog-into.
- M.3 (consistency): console-3 requires an idempotent retry of a unit request, but the request submit route of this change defines no idempotency key. The ADR check lists ADRs 110, 115, 218, 222 and 286, but exit step 1 names ADRs 110 to 117. Resolution: answered: Added requirement Idempotent submit with Idempotency-Key scenario on POST self-service requests; extended adr_check to ADRs 111 to 117.

## Open questions

none

Questions considered and answered:

- Does one blueprint request make one or many units? ADR 110 decision 2 answers it. One request makes one normal stack or workspace. Decisions 6 and 7 answer the order: the server makes the empty unit first through the provisioner port, then starts one job through job.Service.
- How do quotas count units in flight? ADR 115 decisions 4, 5 and 6 answer it. Usage is settled plus reserved, the submit reserves in the same transaction, and applied settles while rejected, canceled, failed or expired releases. Decision 9 answers drift: hourly reconcile from inventory wins.
- Can a request use a blueprint of another namespace? ADR 218 decision 3 answers it. No. A request in namespace A uses only blueprints of A, else the server answers not found.
- Which language expresses the input schema of a blueprint version? ADR 110 decision 8 requires deterministic render with no secret value and named inputs but names no schema language. This change picks JSON Schema on the blueprint version because it gives named-field errors and deterministic validation. Revisions pin the blueprint version so later edits never change an old revision.
- Where do quota rechecks run? ADR 115 decision 10 with ADR 226 decision 11 answers it. Expr quota rules run as gates at change.propose and again at change.apply, units rules recheck with the own reservation counted, requests rules run at submit only. Gate evaluation itself ships in turn-blueprint-rules-catalog-into.
- How does a retry of a request submit stay idempotent? ADR 110 decision 7 answers the job part with idempotency key request ID and revision. This change extends the same key to POST self-service requests: a retry with the same Idempotency-Key returns the same revision, unit and job.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
