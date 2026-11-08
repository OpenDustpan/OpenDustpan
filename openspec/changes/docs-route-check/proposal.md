## Problem

A guide or a reference page can name a route that the built server does not
serve. The blueprint pages are one case: `docs/guides/publish-a-blueprint.md`
and `docs/reference/self-service-api.md` name six blueprint routes, and the
binary answers each one with 404 and the code `server.route-not-found`. A test
that reused the existing route test's server would not catch this, because that
server enables the self-service module and the binary leaves it off. A reader
follows the guide and gets a 404.

## Evidence

- **The route test that refuses a route without an action.**
  `pkg/dustpan/route_actions_test.go:439`
  `func TestEveryRouteDeclaresActionsOrIsPublic(t *testing.T) {` runs
  `checkRouteActions` at `:448`.
- **How that test builds its mux.** It reads the route facts from a server that
  `pkg/dustpan/apidesc_server_test.go:53`
  `func newModuleServer(...)` builds with `dustpan.NewServer(opts...)` at `:80`.
  Line `:67` passes `dustpan.WithSelfServiceBlueprints(...)`, so this test
  server serves the blueprint routes. `TestNoRouteAnswersWithoutACredential` at
  `pkg/dustpan/route_actions_test.go:458` sends requests through
  `srv.UngatedHandler()` at `:465`.
- **How the binary builds its mux.** `cmd/opendustpan/main.go:949`
  `srv, err := dustpan.NewServer(opts...)` uses the options of
  `func (f *serveFlags) serverOptions(...)` at `:1060`. The option list starts at
  `:1088` and holds no `dustpan.WithSelfServiceBlueprints`.
  `pkg/dustpan/selfservice.go:22-24` says: "Without it, the server serves none
  of them."
- **The unserved answer.** `pkg/dustpan/dustpan.go:3621-3624`
  `func v1NotFound(...)` writes
  `http.StatusNotFound, "not found: no route at this path", routeAnswer(errcode.ServerRouteNotFound)`.
  `internal/errcode/catalog_server.go:13` holds
  `ServerRouteNotFound = "server.route-not-found"`.
- **The docs name the blueprint routes.**
  `docs/guides/publish-a-blueprint.md:15`, `:29`, `:40`, `:52` and `:64` hold
  the five curl URLs. `docs/reference/self-service-api.md:18-23` holds the six
  route rows, and `:51` repeats the first route.
- **The blueprint routes are still unserved.** `rg WithSelfServiceBlueprints
  cmd/opendustpan` finds no caller.
- **How the docs write a route today.** A table holds the path then the method
  (`docs/reference/self-service-api.md:18`:
  ``| `/api/dustpan/v1/self-service/blueprints/{namespace}` | `GET` | ... |``).
  A table also holds the method and path in one cell
  (`docs/reference/git-host-links-api.md:49`:
  ``| `POST /me/linked-account-requests` (not available yet) | ... |``).
  A list holds `` `GET /api/dustpan/v1/self-service/blueprints/{namespace}` ``
  (`docs/reference/self-service-api.md:51`). A curl block gives the method in
  `-X` and the URL after `$SERVER` (`docs/guides/publish-a-blueprint.md:27-29`).
  A relative path takes the prefix of the page (`docs/reference/drift-api.md:14`,
  `:20`: `POST /namespaces/{namespace}/units/{unit_id}/drift-checks`) or of the
  section (`docs/reference/issuer-api.md:66`).
- **The marker is already in the docs.**
  `docs/reference/git-host-links-api.md:49` writes `(not available yet)` after
  the route, and `:66` writes it in the heading
  `## Start a request (not available yet)`. The docs give the form of the
  marker.

## Fix boundary

**IN**

- Add `cmd/opendustpan/docs_routes_test.go`.
  - `extractDocRoutes` reads both doc directories. It takes a method and a path
    from a code span `METHOD /path`, from a table row with a path cell and a
    method cell in either order, and from a curl block with `$SERVER` and `-X`
    (no `-X` means GET). A relative path takes the most recent "The paths ...
    start with `<prefix>`" sentence. A path with `...` is skipped.
  - `routeIsMarked` marks a route when its row, its list item or its nearest
    heading holds `(not available yet)`, or when the page says "not available
    yet" for all of its routes.
  - `checkDocRoutes` sends each extracted route to a handler with no credential
    and returns each `server.route-not-found` that the route does not mark, with
    its `path:line`.
  - `TestDocsRoutesAreServed` builds the server from `serveFlags.serverOptions`
    with the default flags, a fresh SQLite store, a static token authenticator
    and a built-in wrapper. It calls `srv.Start`, runs `checkDocRoutes` on
    `srv.Handler()`, and logs each marked route. It does not use the
    `pkg/dustpan` test server, which enables the self-service module.
  - `TestExtractDocRoutes` is table-driven: one case for each form, one for a
    skipped `...` path and one for the marker.
  - `TestDocsRouteCheckFailsOnAnUnservedRoute` fails on a fake unserved route
    and passes on the same route with the marker.
- `docs/reference/self-service-api.md`: add `(not available yet)` to the six
  rows at `:18-23`.
- `docs/guides/publish-a-blueprint.md`: add the sentence "The routes are not
  available yet." after the introduction.
- No Makefile change: `make check` runs `go test ./...` at `Makefile:214-215`.

**OUT**

- No change to the server routes, the handlers or the option set of
  `cmd/opendustpan`.
- No change to the authorization actions or the public list.
- No new Makefile target.
- No spec delta.

## Check

- Guard 1: an unmarked doc route that the binary does not serve fails the test.
  Test: `TestDocsRoutesAreServed`. Mutation: remove `(not available yet)` from
  `docs/reference/self-service-api.md:18`; the request for
  `GET /api/dustpan/v1/self-service/blueprints/zz` answers 404
  `server.route-not-found`, and the test fails with that `path:line`.
- Guard 2: a marked route is skipped and listed. Test:
  `TestDocsRouteCheckFailsOnAnUnservedRoute`. Mutation: ignore the marker in
  `routeIsMarked`; the marked fake route fails.
- Guard 3: each doc form is extracted. Test: `TestExtractDocRoutes`.
  Mutation: remove the curl case from `extractDocRoutes`; the curl case of the
  table test fails with a missing route.

## ADR check

- **ADR 17 decision 6 (not owner-tagged by the rule; the context says the owner
  made the decision).** "A test enforces it. A test lists every route that the
  server registers and fails when a route has no declared action. Routes that
  need no sign-in ... are on an explicit list in the same test." Changed by:
  ADR 30, 210, 222, 224, 266, 305, 326. Current rule: this ADR, with those
  ADRs. The new test does not change that test. No conflict.
- **ADR 326 decision 3 (owner).** "One route test for the whole server (owner).
  One test builds the server and reads its routes from the root mux: the
  registered patterns and the mounted paths. It does not read
  `e2e.ServedRoutes`. It fails when two adapters serve the same route, or when
  a route that is not public declares no action. It holds one public list for
  the whole server." Changed by: none. Current rule: this ADR. The new test is
  a docs check, not the route test, and it does not read the root mux. No
  conflict.
- **ADR 214 decision 1 (not owner-tagged).** "Docs in the same pull request. A
  pull request that adds or changes a feature that a person or a client can see
  adds or changes its page in the user documentation." Changed by: ADR 315
  (extends: who owns the pages). Current rule: this ADR, with ADR 315. The fix
  adds no feature. No conflict.
- **ADR 66 decision 1 (not owner-tagged).** "Code and user documentation
  contain no internal project references." Changed by: ADR 251 (extends: the
  words that copy uses). Current rule: this ADR, with ADR 251. The test file,
  its messages and the marker add no internal reference. No conflict.

No conflict with an owner-tagged decision.

## Open questions

none
