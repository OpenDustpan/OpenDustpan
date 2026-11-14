## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Write a failing test for namespace create with no name in internal/cli. Confirmed by: CI (go test ./internal/cli/). Check: go test ./internal/cli/ fails on main, passes after the fix.
- [ ] 1.2 Write a failing test in internal/api/server that calls POST /api/dustpan/v1/sign-in/passkey/begin behind Handler.passkeyRoutes with a session credential; BLOCKED by gate not named in the pack. Confirmed by: CI (go test ./internal/api/server/). Check: go test ./internal/api/server/ fails on main: the 404 body names no `--public-url`.
- [ ] 1.3 Write a failing test in cmd/opendustpan for empty, malformed, and negative `--plan-retention` at server start. Confirmed by: CI (go test ./cmd/opendustpan/). Check: go test ./cmd/opendustpan/ fails on main: the error names no default and no format.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. definition of done

- [ ] 3.1 Fix the four errors at their sources; the routes keep their authorization actions. Confirmed by: CI (go test ./internal/cli/ ./internal/api/server/). Check: The four tests above pass.
- [ ] 3.2 Add an e2e step that runs the real Pulumi CLI `pulumi stack ls` with an access token against a dev server with no console and checks that no console URL shows; BLOCKED by Pulumi stack-list route not named in the pack. Confirmed by: CI (make e2e). Check: The docs commands run as written in the e2e step.
- [ ] 3.3 Update the docs page for the four errors and check the person path from the docs page alone against GET /api/dustpan/v1/settings with a session credential; BLOCKED by gate not named in the pack. A person's step. Check: The docs commands run as written.
- [ ] 3.4 Run the full check. Confirmed by: CI (make check). Check: `make check` with zero findings.
