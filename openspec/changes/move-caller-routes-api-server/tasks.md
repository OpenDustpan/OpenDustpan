## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write route tests for the six moved routes with an access token of the caller in internal/api/server/caller_routes_test.go (NEW). Check: `go test ./internal/api/server -run TestCallerRoutesMoved` fails on main
- [ ] 1.2 Write the paging test for cursor and limit refusal on GET /api/dustpan/v1/me/tokens in internal/api/server/caller_routes_test.go (NEW). Check: `go test ./internal/api/server -run TestCallerPagingNames` fails on main
- [ ] 1.3 Write the idempotency test for POST /api/dustpan/v1/me/tokens with an access token of the caller in internal/api/server/caller_routes_test.go (NEW). Check: `go test ./internal/api/server -run TestCallerIdempotency` fails on main
- [ ] 1.4 Write the hostile test for a foreign token id and an oversized pageSize in internal/api/server/caller_routes_test.go (NEW). Check: `go test ./internal/api/server -run TestCallerHostile` fails on main

## 2. Move

- [ ] 2.1 Move the handlers and the path constants to the me prefix in internal/api/server/tokens.go:181-182 (ownTokensPath, ownTokenPath), its error text at :523 and its comments at :7 and :643. Move callerAccessPath and callerGrantsPath in internal/api/server/review.go:91-92 and its comments at :38, :47, :574 and :774. Remove six entries from the not-moved list in pkg/dustpan/apidesc_classes_test.go. Check: `go test ./pkg/dustpan -run TestClassCheckFailsClosed`
- [ ] 2.4 Change the reducing-route list in pkg/maintenance/reducing.go:73 so that the Pattern "/api/server/caller/tokens/{id}" becomes DELETE /api/dustpan/v1/me/tokens/{id}. Its declaration is in pkg/dustpan/dustpan.go (mountV1 at :3636) and its adapter is in internal/api/server/tokens.go. The routes GET and POST /api/dustpan/v1/me/tokens use the same declaration and adapter files. Change the error text in pkg/accesstoken/scope.go:51, :130 and :169 to name /api/dustpan/v1/me/tokens. Check: `go test ./internal/api/server -run 'TestReductionShapedRoutesAreOnTheList|TestReducingListMatchesRoutes|TestReducingRoutesAnswerInMaintenanceMode'`
- [ ] 2.5 Change the old caller paths in the tests: internal/api/server/revocations_test.go, token_kind_text_test.go:26, routes_test.go, opaque_denial_test.go, signin_test.go, freshness_test.go, delegation_test.go, review_test.go and fresh_session_test.go. Check: `go test ./internal/api/server`
- [ ] 2.2 Move the coverage rows of /api/server/caller/tokens and /api/server/caller/tokens/{id}, and of the access and grants routes, in e2e/coverage.go to the /api/dustpan/v1/me paths, called with an access token of the caller. Update the docs. Check: `rg -n -F 'caller/' docs e2e internal pkg` returns no hit for the old caller paths
- [ ] 2.3 Declare the 404, 401, 400 and conflict codes with arguments in internal/api/server/tokens.go, with Declare (internal/api/apidesc/body.go:156). The task adds the NEW top-level test `TestCallerCodes` in a `_test.go` file of `internal/errcode`, so that the Check runs it. Check: `go test ./internal/errcode -run TestCallerCodes`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step in e2e/caller_routes_test.go (NEW) that calls GET /api/dustpan/v1/me/access and the routes /api/dustpan/v1/me/tokens and /api/dustpan/v1/me/tokens/{id} with an access token of the caller, and checks the 404 of the old path /api/server/caller/access. The declaration of these routes is in pkg/dustpan/dustpan.go (mountV1 at :3636). The adapter is in internal/api/server/tokens.go for the token routes and in internal/api/server/review.go for the access route and the old path. The apidesc labels of the moved routes join in internal/api/server/operations.go (joinLabels). Check: `make e2e` passes with the real Pulumi CLI and OpenTofu. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Run the full check, and the usability check from the docs page alone. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
