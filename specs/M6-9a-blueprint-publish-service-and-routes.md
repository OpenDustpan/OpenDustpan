# M6-9a: the blueprint service and routes: draft, publish, read, list, retire

- Class: SECURITY (a published blueprint decides what other teams can run)
- Routing: Sonnet author, Opus review
- Branch: `m6-9a-blueprint-publish`
- Conflict group: `selfservice-svc`
- Persona path: P6 self-service, step `publish_pg_v1` (#2610); milestone 6
  exit test step 1 (publish). The form check (422) and the submit are M6-9b
  and later.
- Depends on: none. Independent of M6-8 (a publish renders nothing).

## Facts checked at main 9e96ae30c8c3

- `internal/selfservice/ports.go:87-126`: `BlueprintStore` (`PutDraft`,
  `Draft`, `Publish`, `Version`, `ListBlueprints`, `Retire`,
  `SetExprVersion`), `Publication` (139), `ExprVersionChange` (130).
- SQL adapters exist: `pkg/store/sqlite/self_service_blueprints.go`,
  `pkg/store/postgres/self_service_blueprints.go`
  (`SelfServiceBlueprintStore`), with `RunBlueprints` of
  `internal/selfservice/selfservicetest`.
- No service and no HTTP adapter for blueprints: no `internal/selfservicesvc`,
  no `internal/api/selfservice`. Nothing outside the suite and the stores
  names `selfservice.BlueprintStore`.
- `internal/selfservice/actions.go:203-213`: `simpleRequirements`:
  `OpListBlueprints`/`OpReadBlueprint` -> `ActionBlueprintRead`,
  `OpWriteDraft` -> `ActionBlueprintPublish`, `OpRetire` ->
  `ActionBlueprintRetire`. There is no `OpPublish`.
- `internal/selfservice/blueprint.go`: `ParseBlueprint` (226),
  `Blueprint.Validate` (244), `CheckOneHome` (372), `ValidKey` (297).
- `internal/selfservice/check.go:222`: the `Evaluator` port;
  `internal/expr.New(limiter)`, `(*Evaluator).Version()`, `Compile(source, kind)`.
- ADR 117 decisions 5 (routes), 6 (audit entries), 7 and 8 (limits). ADR 286:
  the route segment is `self-service`, under `/api/dustpan/v1/` (ADR 286 table
  "Route paths in ADRs"). ADR 112 (documents, Expr versions).
- CLAUDE.md: the service lives in `internal/<domain>svc`; every route declares
  its actions; every feature has a REST API (ADR 17).

## Files it may touch

- `internal/selfservice/actions.go` (add `OpPublish` -> `ActionBlueprintPublish` on the blueprint, and its test)
- `internal/selfservicesvc/` (new): `blueprints.go`, tests, `leak_test.go`
- `internal/api/selfservice/` (new): routes, handlers, route test, `leak_test.go`
- `pkg/dustpan/` one new file `area_selfservice.go` for the wiring, plus the
  one line that mounts the handler
- `.golangci.yml`: only if the new packages need a listed layer (the domain
  rule already covers `internal/selfservice/**`; `selfservicesvc` is a service)
- `openapi.json`, `clients/typescript`, `internal/errcode` (by the generators)
- `docs/reference/self-service-api.md` (new), `docs/guides/` a short
  "Publish a blueprint" page

## Files it must not touch

- `internal/selfservice/render/` (M6-8)
- The blueprint store adapters and their SQL, unless a suite case fails on
  them (report first)
- Request routes: options, check, submit, render, cancel (later items)

## Change

1. `selfservicesvc.Blueprints` with `ListBlueprints`, `ReadDraft`,
   `WriteDraft`, `Publish`, `ReadVersion`, `Retire`. Each method checks
   `selfservice.Requirements` for its op with the authorizer, at the point of
   use, for the blueprint object. A denial on a read is `ErrNotFound`.
2. `WriteDraft`: strict parse (`ParseBlueprint`), `Validate`, `CheckOneHome`,
   size limits of ADR 117 decision 7, at version of the caller; audit entry.
3. `Publish`: read the draft at the given draft version, validate again,
   compile each expression with the Evaluator, set `ExprVersion` from
   `Evaluator.Version()`, then `BlueprintStore.Publish` with the audit entry of
   ADR 117 decision 6 (each source and the source principal). Validate again
   even though the draft write validated (comment: defense in depth; the
   store row can come from an older version or a restore).
4. Routes under `/api/dustpan/v1/self-service/`:
   `GET blueprints/{namespace}`, `GET`/`PUT blueprints/{namespace}/{blueprint}/draft`,
   `POST blueprints/{namespace}/{blueprint}/versions`,
   `GET .../versions/{version}`, `POST .../versions/{version}/retire`.
   Strict JSON decode (`internal/textbody`), `ETag` on the draft, errors in the
   tool-neutral shape. Each route row declares its action and object.

## Acceptance

- Service tests, one hostile test for each layer, each alone:
  - boundary: a draft with an unknown field, a duplicate key, a key that
    fails `ValidKey`, a body over the limit: refused, nothing stored.
  - Expr: an expression that does not compile, and one that tries a name
    outside the environment: publish refused, no version.
  - authorization: a principal with `blueprint:read` only cannot write or
    publish; a principal with `blueprint:publish` on blueprint `a` cannot
    publish `b` (fake authorizer for the service; real roles in the route test).
  - store row: a draft row edited in the table to an invalid document is
    refused at publish.
  - fail closed: authorizer error, Evaluator error, store error: deny,
    nothing stored, the denial written to the audit log.
- Route test lists the six routes with their actions; unknown fields 400;
  stale ETag 409/412 as the other tool-neutral APIs do.
- e2e: a publish through the API on SQLite and PostgreSQL. If #2610 is on main,
  un-skip `publish_pg_v1`.
- Docs: `docs/reference/self-service-api.md` and the guide page.
- `make check`, `make clients-check` zero findings.
