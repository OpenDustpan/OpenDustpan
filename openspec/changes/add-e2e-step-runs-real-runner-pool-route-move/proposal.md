Issue: #984.

## Why

**Persona: runner operator.** As a runner operator I run a pool that executes jobs, so I create pools, join runners and retire them through stable tool-neutral routes with clear audit records.

The user path this change must make work:

1. **API.** Create a pool and a join token from the docs page, then list pools and tokens through the API
2. **API.** Read the pool status and release and revoke a token through the API
3. **Docs page.** Follow the docs page for pool work

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A principal without the grant reads or changes pools | {"code": 403, "message": "forbidden: needs namespace:manage-runners on the namespace"} | `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools` |
| A caller keeps the old pool path | {"code": 404, "message": "unknown route: use GET /api/dustpan/v1/namespaces/{namespace}/runner-pools"} | `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools` |
| A request with no credential reaches a pool route | {"code": 401, "message": "sign-in required: sign in and retry"} | `opendustpan status` |

Evidence for the claims above:

- The route coverage file exists at e2e/coverage.go Proof: Pack path e2e/coverage.go exists true
- Issue 984 asks for an e2e step that runs a real runner and launcher for a plan and an apply of each tool Proof: Issue #984 Outcome: make e2e starts the server, joins a real dustpan-runner with a join token, and runs a plan and an apply phase of each supported tool

## What Changes

- Runner management lives at /api/dustpan/v1/namespaces/{namespace}/runner-pools and below with the same actions
- Each pool and join token mutation writes its audit entry in the same transaction
- Reads never return a join secret
- Old pool paths answer 404 with no alias

Out of scope:

- The launcher binary
- The live runner run with join, poll, handoff, session, steps, logs, plan commit and finish
- Drain and retire steps with a live runner
- The skew matrix of older runner releases
- Moves of inventory and context routes

## Usability

Where the path still costs the person time or surprises them:

- Operators of the old pool paths must edit scripts to the new paths; the 404 names the new path.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 138: Runner actions, audit entries and the REST API** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 6 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "Two new actions. They join the actions of ADR 103 (`runner.ActionNamespaceReadRunners`, `runner.ActionRunnerJoin`):"
- Quote: "Audit entries. Each of these writes one audit entry in the transaction of the change (`runner.AuditRules`):"
- Quote: "`runner-drained`, `runner-retired` | the principal | `namespace:manage-runners` | `change`"
- Quote: "Routes. The tool-neutral OpenDustpan API gets these routes. Each route lands with its e2e step:"
- Effect: Keeps the actions of decision 1 and the audit events of decision 4. Moves the management paths of decision 6 to the tool-neutral prefix with no alias.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: Places runner management under /api/dustpan/v1 and keeps /runner/v1 outside it.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The ADR check quotes ADR 210 decision 2 and cites the rule that moves the sign-in callback, but the change does not say whether the 404 for the old pool paths also applies to other moved routes such as inventory and context routes named in ADR 247 G1; the move scope is not fixed in the change (proposal.md, design.md). Resolution: answered: Design now fixes the 404 rule to the pool and join-token paths only and lists inventory and context moves as out of scope.
- R1.2 (wrong claim): The ADR check says ADR 138 decision 6 routes are the tool-neutral routes, but the proposal's rule that the 404 names the new path and that the audit entries are written 'in the same transaction' is asserted without the ADR 138 decision 4 audit table quote for runner-drained and runner-retired subjects; the quoted decision line for audit events is truncated in the input, so the claim cannot be checked (proposal.md ADR check). Resolution: answered: ADR check now quotes the drained and retired audit row and limits the effect to the quoted rows.
- R1.3 (missing scenario): No scenario covers a join-token secret read after a failed or partial create transaction, or a token revoke whose audit write fails; the spec only states the success path for create-token and pool-created audit. Resolution: answered: Specs now cover failed create with no secret leak and failed audit write with rollback.
- R1.4 (missing scenario): No scenario covers PATCH and DELETE on a pool that a runner still holds, or drain and retire of a runner, although the requirement names them; the spec has no scenario for those mutations. Resolution: answered: Specs now cover pool change and delete, and drain and retire of a runner.
- R1.5 (failure mode): Pool ID and join token ID are used in routes, but the change does not say that a pool renamed or removed and made again uses the stable ID, as the project convention requires for stored references. Resolution: answered: Design and specs now state stable IDs for pools and join tokens.
- R1.6 (failure mode): The public list is said to be unchanged, but the spec does not state how the route behaves for an unauthenticated request to a pool route (401) versus a principal without the grant (403), though the proposal's error table lists both. Resolution: answered: Specs now state 401 for no credential and 403 for no grant.
- R1.7 (scope): Task 2.3 requires make e2e route coverage with the new paths, but the change does not say whether the e2e step for the new routes is in scope, while ADR 138 decision 6 and issue 984 require each route to land with its e2e step. Resolution: answered: Tasks and out of scope now state that this part updates the served-routes list and route tests, and the live runner run ships later.
- R1.8 (open question): The proposal is not spec-ready: the evidence paths cmd/dustpan-runner and the command opendustpan auth do not exist, yet the error table uses opendustpan auth sign-in as the helping command. The change must name a command that exists before the gate. Resolution: answered: Evidence cites only the existing path e2e/coverage.go and the error table uses opendustpan status and API reads.

## Open questions

none

Questions considered and answered:

- Does the move keep an alias on the old pool paths? No. The split scope keeps the same actions with no alias, and ADR 210 places management routes under /api/dustpan/v1. The 404 names the new pool path.
- Does the 404 rule cover other moved routes such as inventory or context routes? No. This change moves only runner pool and join-token management. Inventory and context route moves stay out of scope.
- Does each moved route land with a live-runner e2e run in this part? Route tests and the served-routes list update ship here. The live run with a real runner and launcher ships in the later parts of the split.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
