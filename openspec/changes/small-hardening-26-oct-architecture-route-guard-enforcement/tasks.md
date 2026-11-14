## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing guard tests for runner, jobs and drift through `internal/api/route/enforce.go:102` in NEW `internal/api/route/enforce_test.go`. Confirmed by: CI (go test ./internal/api/route/...). Check: NEW TestRouteGuardSingleAuthorizePath fails on main, passes after the change.
- [ ] 1.2 Add failing address-order tests for Authenticate, address check, Resolve in `internal/api/route/enforce.go:102` in NEW `internal/api/route/enforce_test.go`. Confirmed by: CI (go test ./internal/api/route/...). Check: NEW TestRouteAddressForbiddenBeforeResolve fails on main, passes after the change.

## 2. Change

- [ ] 2.1 Fold `internal/api/route/route.go:102` Guard.AuthorizeAll into the `internal/api/route/authorize.go:54` Guard.AuthorizeObject path. Confirmed by: CI (go test ./internal/api/route/...). Check: `rg AuthorizeAll internal/api/route/route.go` returns no route callers after the fold.
- [ ] 2.2 Wire runner, jobs and drift onto `internal/api/route/enforce.go:102` route.Enforce and delete the three caller address-check copies. Confirmed by: CI (go test ./internal/api/...). Check: GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token from a denied address answers route.address-forbidden.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Confirm the route GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token and its authorization actions. Confirmed by: CI (go test ./pkg/dustpan/...). Check: Route test lists authorization actions for GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token.
- [ ] 4.2 Add an e2e step on GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token and check the address-forbidden denial. Confirmed by: CI (make e2e). Check: e2e step calls GET /api/dustpan/v1/namespaces/{}/units/{}/drift with an access token and checks the address-forbidden denial.
- [ ] 4.3 Update the user documentation for the guard order and allowlist command in the same pull request. Confirmed by: CI (make docs-lint). Check: Docs page for the address-forbidden answer and the allowlist read command ships in the same pull request.
- [ ] 4.4 Record where the path still costs time or surprises with an access token from the docs page alone. A person's step. Check: Usability check with an access token from the docs page alone.
- [ ] 4.5 Run the full check gate with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings.
