## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 List each /api/dustpan/v1 list route that today accepts limit or cursor, starting from internal/api/server/principals.go and internal/api/server/namespaces.go. Check: `rg -n 'limit|cursor' internal/api/server/principals.go internal/api/server/namespaces.go internal/api/registry/handlers.go` lists each in-scope route before edit.
- [ ] 1.2 In internal/registry/service/read_test.go:270 add failing tests for aliases, conflict precedence, defaults and maximum, dual tokens, forged tokens, authorization and store-side provider paging, using t.Context(). Check: TestProviders alias, conflict, default, forged-token, and paging cases fail on main and pass after the change.

## 2. Implement

- [ ] 2.1 In internal/registry/service/read.go:239 accept pageSize and pageToken, add namespace.ParseName to Service.Providers with a defense-in-depth comment, and change Addresses in pkg/store/postgres/registry.go:249 and the SQLite adapter to after plus limit keyset paging in byte-wise address order. Check: `go test ./internal/registry/service/... ./pkg/store/...` passes on Postgres and SQLite dialects.
- [ ] 2.2 Declare pageSize and pageToken in internal/api/registry/operations.go:34 and in the apidesc operations of the principal and namespace list routes, with old names marked deprecated. Check: `go test ./internal/registry/service/...` passes and openapi.json lists pageSize and pageToken on each in-scope list route.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire GET /api/dustpan/v1/namespaces/{}/registry/providers with its authorization actions, run the e2e list step with an access token, update the docs page, run a usability check, and run make check with zero findings. Check: GET /api/dustpan/v1/namespaces/{}/registry/providers with an access token pages with pageSize and pageToken; `make check` reports zero findings. Confirmed by: CI (make e2e and make check).
