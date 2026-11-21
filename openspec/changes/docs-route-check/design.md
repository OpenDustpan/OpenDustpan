## Context

A guide or a reference page can name a route that the built server does not serve. The binary answers 404 with code `server.route-not-found` on such a route. A reader follows the guide and gets a 404.

This change adds no server code, no route, and no option. It adds a test in `cmd/opendustpan` that reads the docs pages and sends each named route to a server built through the SDK. No spec delta.

## Decisions

1. **One extractor for every doc form.** The NEW helper `extractDocRoutes` in `cmd/opendustpan/docs_routes_test.go` reads `docs/reference` and `docs/guides`. It takes a method and a path from a code span `METHOD /path`, from a table row with a path cell and a method cell in either order, and from a curl block with `$SERVER` and `-X` (no `-X` means GET). The prefix rule has three parts, in order, with no list of paths: a path that starts with `/api/` is absolute and takes no prefix; any other path takes the prefix of the nearest sentence in scope that sets one; a path with no such sentence is used as written. A path with `...` is skipped. The helper reads through `os.Root`, so a page outside the repository root cannot be reached.
2. **One marker for routes that no option mounts.** `routeIsMarked` marks a route only when its row, its list item, or its nearest heading holds `(not available yet)`. There is no page-level marker. A marked route is skipped and logged with its `path:line`.
3. **The test builds the server through the SDK with every option on.** `TestDocsRoutesAreServed` builds the server with `dustpan.NewServer` on a fresh SQLite store, not through the flags and the environment of the binary. It turns on each optional feature through its option: the public URL, the LDAP provider with a fake bind password from `internal/fakecred`, an OpenID Connect provider on a test issuer, and the blueprint store. It sends each extracted route with no credential and fails when the body holds `server.route-not-found`. Each route that needs optional config is mounted by its option, so no such route is marked and none is skipped.

## Structure

No new server structure. The bug class is a docs page that names an unserved route. The test helpers `extractDocRoutes`, `routeIsMarked`, and `checkDocRoutes` are unexported functions of the test file; only the tests call them. No lint setting is added. The existing route test keeps its public list and its action check.

No custom analyzer and no new tool. No Makefile change: `make check` runs the Go tests.

## Risks

- A new docs form that the extractor does not parse is silently missed. The table test `TestExtractDocRoutes` holds one case for each form, and a new form needs a new case first.
- A new optional feature needs its option in the test server, or its routes look unserved. The test asserts by name that each route behind an option answers without `server.route-not-found`, so the missing option fails loudly.
