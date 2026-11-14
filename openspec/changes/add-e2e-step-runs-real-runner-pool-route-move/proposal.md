---
version: 2
---

Issue: #984.

## Why

**Persona: runner operator.** As a runner operator I run a pool that executes jobs, so I create pools, join runners and retire them through stable tool-neutral routes with clear audit records.

The user path this change must make work:

1. **API.** Create a pool and a join token, then list pools and tokens through the API
2. **API.** Read the pool status and release, drain and retire a runner, and revoke a token through the API
3. **Docs page.** Follow the docs page for pool work

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A request with no credential reaches a pool route | {"code": 401, "message": "sign-in required: sign in and retry"} | `opendustpan status` |
| A principal without the read grant lists or reads pools | {"code": 403, "message": "forbidden: needs namespace:read-runners on the namespace"} | `opendustpan status` |
| A principal without the manage grant changes pools, tokens, drain, or retire | {"code": 403, "message": "forbidden: needs namespace:manage-runners on the namespace"} | `opendustpan status` |
| A caller keeps the old pool path | {"code": 404, "message": "unknown route: use GET /api/dustpan/v1/namespaces/{namespace}/runner-pools"} | `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools` |

Evidence for the claims above:

- The route coverage file exists Proof: Pack path e2e/coverage.go exists true; ServedRoutes at e2e/coverage.go:42
- Main serves runner management under the old prefix Proof: Pack route list: ANY /api/runners/{}/pools, /api/runners/{}/runners/{}/drain, /api/runners/{}/runners/{}/retire, /api/runners/{}/pools/{}/join-tokens/{}/revoke
- Runner error codes exist on main Proof: internal/errcode/catalog_runner.go:8
- The split scope sets the new path and no alias Proof: Roadmap step of the pack: moves runner management to /api/dustpan/v1/namespaces/{namespace}/runner-pools with the same actions and no alias

## What Changes

- Pool routes move to GET and POST /api/dustpan/v1/namespaces/{namespace}/runner-pools
- GET, PATCH and DELETE on /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}
- Runner drain and retire routes move under the runner-pools path
- Join-token list, create and revoke routes move under the runner-pools path
- Each mutation writes its audit entry in the same transaction
- Old pool paths answer 404 with no alias

Out of scope:

- The launcher binary
- The real runner e2e step with join, poll, handoff, session, steps, logs, plan commit and finish
- The skew matrix of older runner releases
- Moves of inventory and context routes
- /runner/v1 protocol changes

## Usability

Where the path still costs the person time or surprises them:

- Scripts that use the old pool paths must change once; the 404 names the new path.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ServedRoutes lives in the e2e package (`e2e/coverage.go:42 (func ServedRoutes)`)
- Runner error codes exist on main (`internal/errcode/catalog_runner.go:8`)
- No dustpan-runner binary exists on main (`cmd/dustpan-runner (path does not exist)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 138: Runner actions, audit entries and the REST API** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 6 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "Two new actions. They join the actions of ADR 103 (`runner.ActionNamespaceReadRunners`, `runner.ActionRunnerJoin`):"
- Quote: "Audit entries. Each of these writes one audit entry in the transaction of the change (`runner.AuditRules`):"
- Quote: "Routes. The tool-neutral OpenDustpan API gets these routes. Each route lands with its e2e step:"
- Effect: Keeps the actions of decision 1 and the audit events of decision 4. Decision 6 lists the paths under /api/runners; this change serves the same operations under the versioned prefix. The new path comes from the split scope of the item, and decision 6 is changed in its path column only. Open question 1 asks the owner or PM to confirm this.
- Conflict: rfc_needed.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: Keeps /runner/v1 outside the prefix. Management is not a protocol endpoint that an external system stores, so it belongs under /api/dustpan/v1.
- Conflict: none.

## Open questions

1. Does ADR 138 decision 6 need an amendment for the new path prefix? Decides: PM.
   - Options: Record the path change in a later ADR that changes decision 6; Keep the old paths and skip the move.
   - Recommended: Record the path change in a later ADR that changes decision 6 Reason: Decision 6 names /api/runners paths, and the pack has no ADR line that sets the new path.
2. Do join tokens expire, allow one use, or bind to one pool? Decides: PM.
   - Options: No expiry, multi-use until revoked; One use with a short expiry bound to one pool.
   - Recommended: One use with a short expiry bound to one pool Reason: ADR 138 and the pack do not state it; the glossary says a join token lets one runner join one pool one time.
3. Which error code does the 404 on old pool paths carry? Decides: PM.
   - Options: The generic unknown-route code; A runner-specific code.
   - Recommended: The generic unknown-route code Reason: The pack lists runner codes but no unknown-route code.

Not spec-ready: a code check fails: route `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools` (.errors[3].answer) is not in the API description or the route table; use an existing route (nearest: /api/server/namespaces/{}/unit-gra; a code check fails: route `POST /api/dustpan/v1/namespaces/{namespace}/runner-pools` (.what_changes[0]) is not in the API description or the route table; use an existing route (nearest: /api/server/namespaces/{}/unit-gra; a code check fails: route `/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}` (.what_changes[1]) is not in the API description or the route table; use an existing route (nearest: /api/server/namespaces/{}/unit-g; a code check fails: and 6 more; the change conflicts with ADR 138: an RFC comes first.

Questions considered and answered:

- Does the move keep an alias on the old pool paths? No. The split scope says no alias.
- Do other moved routes get the 404 rule? No. Only pool, runner drain and retire, and join-token paths.
- Does a live runner e2e run ship here? No. It ships in a later part of the split (ADR 138 decision 6 asks for it there).

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
