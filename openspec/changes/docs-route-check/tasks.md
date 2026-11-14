## 1. Extraction and check

- [ ] 1.1 Add `cmd/opendustpan/docs_routes_test.go` with `extractDocRoutes`,
  `routeIsMarked`, `checkDocRoutes` and `TestExtractDocRoutes`. The table has
  one case for each form: table path then method, table method and path, inline
  method and path, curl `$SERVER` with `-X`, a relative path with the page
  prefix, a `...` path that is skipped, and the marker. Check:
  `go test ./cmd/opendustpan -run TestExtractDocRoutes` passes.
- [ ] 1.2 Add `TestDocsRoutesAreServed`. It builds the server from
  `serveFlags.serverOptions` with the default flags and a fresh SQLite store,
  calls `Start`, runs `checkDocRoutes` on `srv.Handler()`, and fails when the
  body holds `server.route-not-found`. It logs each marked route with its
  `path:line`. Check: `go test ./cmd/opendustpan -run TestDocsRoutesAreServed`.
- [ ] 1.3 Add `TestDocsRouteCheckFailsOnAnUnservedRoute`. A fake page names an
  unserved route, and the check fails. The same route with the marker passes.
  Check: the test passes.

## 2. Docs

- [ ] 2.1 Run `TestDocsRoutesAreServed` and record each unserved route that it
  names. Check: the output lists each route with `path:line`.
- [ ] 2.2 Mark each unserved route with the docs' marker `(not available yet)`.
  Add it to the six rows of `docs/reference/self-service-api.md:18-23` and add
  the sentence "The routes are not available yet." to
  `docs/guides/publish-a-blueprint.md`. Change no route and serve no new route.
  Check: the test passes and its output lists the marked routes.
- [ ] 2.3 `make docs-lint` reports zero findings. Check: the command exits 0.

## 3. Verification

- [ ] 3.1 Prove guard 1: remove the marker from
  `docs/reference/self-service-api.md:18`; `TestDocsRoutesAreServed` fails and
  names the line and `server.route-not-found`. Check: the failure text holds
  both.
- [ ] 3.2 Prove guard 2: ignore the marker in `routeIsMarked`;
  `TestDocsRouteCheckFailsOnAnUnservedRoute` fails. Check: the test names the
  marker.
- [ ] 3.3 `make check` reports zero findings. Check: the command exits 0.
