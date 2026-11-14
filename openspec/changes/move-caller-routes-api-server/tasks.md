## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write route tests for the six moved routes with a personal access token. NEW test. Check: `go test ./internal/api/server -run TestCallerRoutesMoved` (NEW) fails on main
- [ ] 1.2 Write the paging test for cursor and limit refusal on GET /api/dustpan/v1/me/tokens (NEW path). NEW test. Check: `go test ./internal/api/server -run TestCallerPagingNames` (NEW) fails on main
- [ ] 1.3 Write the idempotency test for POST /api/dustpan/v1/me/tokens (NEW path) with a personal access token. NEW test. Check: `go test ./internal/api/server -run TestCallerIdempotency` (NEW) fails on main
- [ ] 1.4 Write the hostile test for a foreign token id and an oversized pageSize. NEW test. Check: `go test ./internal/api/server -run TestCallerHostile` (NEW) fails on main

## 2. Move

- [ ] 2.1 Move the handlers to the me prefix and remove six entries from the not-moved list. Check: `go test ./pkg/dustpan -run TestClassCheckFailsClosed`
- [ ] 2.2 Move the coverage rows of the four routes /api/server/caller/access, /api/server/caller/grants, /api/server/caller/tokens and /api/server/caller/tokens/{} in e2e/coverage.go, with a personal access token, and update the docs. Check: `rg -n -F '/api/server/caller' docs e2e internal pkg` returns no hit
- [ ] 2.3 Declare the 404, 401, 400 and conflict codes with arguments in the catalog. NEW test. Check: `go test ./internal/errcode -run TestCallerCodes` (NEW)

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step with a personal access token that calls GET /api/dustpan/v1/me/access (NEW path) and the token routes, and checks the 404 of /api/server/caller/access. Check: `make e2e` passes with the real Pulumi CLI and OpenTofu
- [ ] 4.2 Run the full check, and the usability check from the docs page alone. Check: `make check` with zero findings
