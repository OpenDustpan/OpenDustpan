---
version: 1
---

Issue: #1996.

## Problem

A developer runs tools against the server from allowed networks. Three authorize paths and three address-check copies make one denial hard to predict. One guard path gives one predictable denial with the one command that helps.

## Evidence

- The issue asks for one route guard entry point with runner, jobs and drift moved onto route.Enforce. Proof: issues #1996 body item 3: Three ways to authorize a route. Fix: one entry point. route.Enforce takes Checks, AuthorizeAll folds into AuthorizeObject, and runnerapi, jobs and drift move onto route.Enforce.
- route.Enforce exists on main and tool-neutral routes call it. Proof: internal/api/objectcontext/routes.go:138: return route.Enforce(h.kit(), h.writeError, route.Steps[target]{
- Guard.AuthorizeAll and Guard.AuthorizeObject both exist on main as separate paths. Proof: internal/api/route/route.go:102 (func Guard.AuthorizeAll) and internal/api/route/authorize.go:54 (func Guard.AuthorizeObject)
- checkedErrorVariants already refuses a duplicate, so variant-name work is outside this part. Proof: internal/api/apidesc/errorvariants.go:75:// checkedErrorVariants returns the variants sorted by code. It refuses a

## Fix boundary

- route.Enforce runs the allowlist check between Authenticate and Resolve on namespaced tool-neutral routes with the NEW error code route.address-forbidden
- Runner, jobs and drift routes move onto route.Enforce and their three address-check copies are deleted
- Guard.AuthorizeAll folds into the Guard.AuthorizeObject path so one entry point authorizes each route

Out of scope:

- Audit old-value bound and SHA-256 form
- Audited namespace-setting write helpers and purge checks
- Error-variant duplicate-name behavior beyond the existing refuse
- Stack sweep soft-delete and purge blob behavior
- Proxy header trust for client address

## Check

- Add failing guard tests for runner, jobs and drift through `internal/api/route/enforce.go:102` in NEW `internal/api/route/enforce_test.go`. Confirmed by: CI (go test ./internal/api/route/...). Check: NEW TestRouteGuardSingleAuthorizePath fails on main, passes after the change.
- Add failing address-order tests for Authenticate, address check, Resolve in `internal/api/route/enforce.go:102` in NEW `internal/api/route/enforce_test.go`. Confirmed by: CI (go test ./internal/api/route/...). Check: NEW TestRouteAddressForbiddenBeforeResolve fails on main, passes after the change.
- Fold `internal/api/route/route.go:102` Guard.AuthorizeAll into the `internal/api/route/authorize.go:54` Guard.AuthorizeObject path. Confirmed by: CI (go test ./internal/api/route/...). Check: `rg AuthorizeAll internal/api/route/route.go` returns no route callers after the fold.
- Wire runner, jobs and drift onto `internal/api/route/enforce.go:102` route.Enforce and delete the three caller address-check copies. Confirmed by: CI (go test ./internal/api/...). Check: GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token from a denied address answers route.address-forbidden.
- Confirm the route GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token and its authorization actions. Confirmed by: CI (go test ./pkg/dustpan/...). Check: Route test lists authorization actions for GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token.
- Add an e2e step on GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token and check the address-forbidden denial. Confirmed by: CI (make e2e). Check: e2e step calls GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token and checks the address-forbidden denial.
- Update the user documentation for the guard order and allowlist command in the same pull request. Confirmed by: CI (make docs-lint). Check: Docs page for the address-forbidden answer and the allowlist read command ships in the same pull request.
- Record where the path still costs time or surprises with an access token from the docs page alone. A person's step. Check: Usability check with an access token from the docs page alone.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Guard.AuthorizeAll exists as a separate authorize path. (`internal/api/route/route.go:102 (func Guard.AuthorizeAll)`)
- Guard.AuthorizeObject exists as a separate authorize path. (`internal/api/route/authorize.go:54 (func Guard.AuthorizeObject)`)
- route.Enforce exists and tool-neutral routes call it. (`internal/api/route/enforce.go:102 (func Enforce)`)
- checkedErrorVariants already refuses a duplicate generated name. (`internal/api/apidesc/errorvariants.go:79 (func checkedErrorVariants)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- How is the single route guard reached? The issue body item 3 directs one entry point: route.Enforce takes Checks, AuthorizeAll folds into AuthorizeObject, and runnerapi, jobs and drift move onto route.Enforce. Audit old values, settings helpers, purge checks and variant names stay in the other two parts.
- Does an ADR answer the guard shape? The pack names no ADR and gives no decision text for this part. The rg evidence shows only code facts for route.Enforce, Guard.AuthorizeAll, Guard.AuthorizeObject and checkedErrorVariants. No owner-tagged decision is changed.
- How is the client address derived? Address is the direct TCP peer address from the server listener, not X-Forwarded-For. Proxy header trust is out of scope and unchanged by this part.
- Does address denial leak namespace existence? Authenticate runs first, so an unauthenticated caller gets the auth denial. An authenticated caller from a denied address gets the NEW error code route.address-forbidden. The namespace name from the path is confirmed by that answer, but Resolve is skipped so object existence is not confirmed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
