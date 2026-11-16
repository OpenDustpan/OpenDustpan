## 1. Extraction and check

- [ ] 1.1 Add the NEW file `cmd/opendustpan/docs_routes_test.go` with `extractDocRoutes`,
  `routeIsMarked`, `checkDocRoutes` and `TestExtractDocRoutes`. The table has
  one case for each form: table path then method, table method and path, inline
  method and path, curl `$SERVER` with `-X`, a relative path with the page
  prefix, a `...` path that is skipped, and the marker. Check:
  `go test ./cmd/opendustpan -run TestExtractDocRoutes` passes.
- [ ] 1.2 In the NEW file `cmd/opendustpan/docs_routes_test.go`, add `TestDocsRoutesAreServed` (NEW). It builds the server through the SDK, `dustpan.NewServer` (pkg/dustpan) with a fresh SQLite store, not through the flags and the environment of the binary: it turns on each optional feature through its option, so the test sets no environment variable: `dustpan.WithPublicURL` (pkg/dustpan/dustpan.go:455) with an `https` URL, which the console sign-in and the passkey service need (the passkey service comes on from the public URL on a store that keeps passkeys, pkg/dustpan/area_mfa.go:50, and the SQLite store keeps them); `dustpan.WithLDAPProvider` (pkg/dustpan/ldap.go:68) with a `dustpan.LDAPProvider` whose bind password field comes from `internal/fakecred`; `dustpan.WithOIDCProvider` (pkg/dustpan/signin.go:50) with the issuer of an `httptest.Server` in the test, when a documented provider route needs an OpenID Connect provider; and `dustpan.WithSelfServiceBlueprints` (pkg/dustpan/selfservice.go:26) on the store. The test asserts by name that each of these routes answers with no `server.route-not-found`: the 6 blueprint routes, the 5 sign-in provider routes, the 2 passkey sign-in routes, `GET /api/server/session`, `POST /api/server/sign-out` and the 4 passkey routes under `/me/passkeys`. The prefix rule is the one in the proposal: a path that starts with `/api/` is absolute, any other path takes the prefix of the nearest sentence in scope (page or section), and a path with none is used as written. It calls `Start`, runs `checkDocRoutes` on the handler of `(*Server).Handler` (pkg/dustpan/dustpan.go:1992), and fails when the body holds `server.route-not-found`. Each route that needs optional config is mounted by its option, so no such route is marked and none is skipped. It logs each marked route with its `path:line`. Check: `go test ./cmd/opendustpan -run TestDocsRoutesAreServed`.
- [ ] 1.3 In the NEW file `cmd/opendustpan/docs_routes_test.go`, add `TestDocsRouteCheckFailsOnAnUnservedRoute` (NEW). A fake page names an
  unserved route, and the check fails. The same route with the marker passes.
  Check: the test passes.

## 2. Docs

- [ ] 2.1 Run `TestDocsRoutesAreServed` in `cmd/opendustpan/docs_routes_test.go` (NEW) and record each unserved route that it
  names. Check: the output lists each route with `path:line`.
- [ ] 2.2 In `docs/reference/self-service-api.md` and `docs/guides/publish-a-blueprint.md`, mark with the docs' marker `(not available yet)` only a route that main serves under no option at all (a route behind an option is mounted by task 1.2 and gets no marker). `make docs-lint` passes on each edited guide.
  The six blueprint rows of `docs/reference/self-service-api.md:18-23` get no
  marker: `dustpan.WithSelfServiceBlueprints` mounts them in task 1.2. Change
  no route and serve no new route. Check: the test passes, and its output lists
  each marked route, or no route.
- [ ] 2.2b In `docs/reference/settings-api.md:218`, write the route `/health/ready` with no method (``the route `/health/ready` answers 503 to a GET request``). Add no marker. Change no other line. Check: `TestDocsRoutesAreServed` reports no `server.route-not-found` for `docs/reference/settings-api.md`.
- [ ] 2.2a In `docs/guides/backup-restore-upgrade.md:74`, change `GET /api/server/backups/{id}` to `GET /api/dustpan/v1/backups/{id}`, the route that `internal/api/server/backups.go:31-33` serves. Add no marker. Change no other line. Check: `TestDocsRoutesAreServed` reports no `server.route-not-found` for `docs/guides/backup-restore-upgrade.md`.
- [ ] 2.3 `make docs-lint` reports zero findings on `docs/reference/self-service-api.md`, `docs/guides/publish-a-blueprint.md`, `docs/guides/backup-restore-upgrade.md` and `docs/reference/settings-api.md`. Check: the command exits 0. Confirmed by: CI (the agent runs the fast checks only). Credential: none. BLOCKED by: none.

## 3. Verification

- [ ] 3.1 Prove guard 1: remove `dustpan.WithSelfServiceBlueprints` from the options of
  `TestDocsRoutesAreServed`; the test fails and names
  `docs/reference/self-service-api.md:18` and `server.route-not-found`. Restore
  the option. Check: the failure text holds both.
- [ ] 3.1a In the pull request body, record the known gap: the binary mounts no
  blueprint route, and the six routes answer 404 `server.route-not-found` there.
  Check: the body holds the sentence.
- [ ] 3.2 Prove guard 2 in `cmd/opendustpan/docs_routes_test.go` (NEW): ignore the marker in `routeIsMarked` (NEW);
  `TestDocsRouteCheckFailsOnAnUnservedRoute` fails. Check: the test names the
  marker.
- [ ] 3.3 `make check` reports zero findings on `cmd/opendustpan/docs_routes_test.go` (NEW). Check: the command exits 0. Confirmed by: CI (the agent runs the fast checks only). Credential: none. BLOCKED by: none.
