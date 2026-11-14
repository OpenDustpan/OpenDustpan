---
version: 9
---

Issue: #984.

## Why

**Persona: runner operator.** As a runner operator I run a pool that executes jobs, so I create pools, join runners and retire them through tool-neutral routes under the versioned prefix, with an audit record for each change.

The user path this change must make work:

1. **API.** Create a pool and a join token, then list pools and tokens through the runner-pools routes under /api/dustpan/v1/namespaces/{namespace}
2. **API.** Read the pool status and release, drain and retire a runner, and revoke a token through the same routes
3. **Docs page.** Follow the docs page for pool, drain, retire and join-token work

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A request with no credential reaches a pool route | {"code": 401, "message": "sign-in required: sign in and retry"} | `opendustpan status` |
| A principal without the read grant lists or reads pools | {"code": 403, "message": "forbidden: needs namespace:read-runners on the namespace"} | `opendustpan status` |
| A principal without the manage grant changes pools, tokens, drain or retire | {"code": 403, "message": "forbidden: needs namespace:manage-runners on the namespace"} | `opendustpan status` |
| A caller keeps the old pool path | {"code": 404, "message": "unknown route: use the runner-pools path under /api/dustpan/v1/namespaces/{namespace}"} | `opendustpan status` |

Evidence for the claims above:

- The route coverage file exists Proof: Pack path e2e/coverage.go exists true; ServedRoutes at e2e/coverage.go:42
- Main serves runner management under the old prefix Proof: Pack route list: ANY /api/runners/{}/pools, /api/runners/{}/runners/{}/drain, /api/runners/{}/runners/{}/retire, /api/runners/{}/pools/{}/join-tokens/{}/revoke
- Runner error codes exist on main Proof: internal/errcode/catalog_runner.go:8
- The split scope sets the new path and no alias Proof: Roadmap step of the pack: moves runner management to /api/dustpan/v1/namespaces/{namespace}/runner-pools with the same actions and no alias

## What Changes

- Add GET and POST /api/dustpan/v1/namespaces/{namespace}/runner-pools (NEW paths)
- Add GET, PATCH and DELETE /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool} (NEW paths)
- Runner drain and retire move to POST /api/dustpan/v1/namespaces/{namespace}/runners/{runner}/drain and POST /api/dustpan/v1/namespaces/{namespace}/runners/{runner}/retire (NEW paths). A runner ID is unique in its namespace, so a runner is an object of the namespace (ADR 210 decision 3, the row "An object of a namespace": the collection of the object under the namespace, then its ID), and `pkg/dustpan/apidesc_classes_test.go:168-169` already maps the old paths to these.
- The runners of a pool move from GET /api/runners/{namespace}/pools/{pool}/runners (`internal/api/runnerapi/routes.go:80`) to GET /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/runners (NEW path; the parent comes first)
- Join-token list, create and revoke routes move under the runner-pools path (NEW paths)
- Each mutation writes its audit entry in the same transaction
- Remove /api/runners/{}/pools, /api/runners/{}/runners/{}/drain, /api/runners/{}/runners/{}/retire and /api/runners/{}/pools/{}/join-tokens/{}/revoke; they answer 404 with no alias

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

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 138: Runner actions, audit entries and the REST API** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 6 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "Two new actions. They join the actions of ADR 103 (`runner.ActionNamespaceReadRunners`, `runner.ActionRunnerJoin`):"
- Quote: "Audit entries. Each of these writes one audit entry in the transaction of the change (`runner.AuditRules`):"
- Quote: "Routes. The tool-neutral OpenDustpan API gets these routes. Each route lands with its e2e step:"
- Effect: Keeps the actions of decision 1 and the audit events of decision 4. The paths of decision 6 are changed by ADR 210, the current rule: the change serves them at the paths that ADR 210 gives.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 and 3 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: Keeps /runner/v1 outside the prefix. Management is not a protocol endpoint that an external system stores, so it belongs under /api/dustpan/v1.
- Quote (decision 3): "| An object of a namespace | `/api/dustpan/v1/namespaces/{namespace}/<collection>[/{id}]`, for example `.../namespaces/{namespace}/change-orders/{id}`, `.../namespaces/{namespace}/runner-pools/{id}` |"
- Effect: a runner is an object of its namespace, so drain and retire are under `.../namespaces/{namespace}/runners/{runner}`; the runners of a pool are under the pool, the parent first (PM decision, rule 16, AIP-122).
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the move keep an alias on the old pool paths? No. The split scope says no alias.
- Do other moved routes get the 404 rule? No. Only pool, runner drain and retire, and join-token paths.
- Does a live runner e2e run ship here? No. It ships in a later part of the split.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
