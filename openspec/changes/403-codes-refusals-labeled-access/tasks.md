## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for job token refusal mapping at pkg/job/service_start.go Check: NEW TestJobTokenRefusalCode in pkg/job/service_start_test.go fails on main with access.denied
- [ ] 1.2 Add failing tests for connection and state guard refusal mappings at pkg/dustpan/jobservice.go and pkg/dustpan/stateguard.go Check: NEW TestConnectionRefusalCode in pkg/dustpan/jobservice_test.go and NEW TestStateGuardRefusalCode in pkg/dustpan/stateguard_test.go fail on main with access.denied
- [ ] 1.3 Add failing tests for gate store-error code, mapper order, and forbidden order at internal/errcode/errcode_test.go:26 Check: NEW TestGateStoreErrorCode and NEW TestForbiddenOrderWithoutCsrfPin in internal/errcode/errcode_test.go fail on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Replace the ErrDenied wraps at the four call sites with distinct NEW sentinels that wrap ErrDenied Check: go test ./pkg/job/... ./pkg/dustpan/... ./pkg/gate/... passes
- [ ] 3.2 Add the NEW codes job.token-refused, connection.unusable, state.subject-refused, gate.store-unavailable to the adapter-side errcode mapping after errcode-catalog-imports-domain-packages and fix TestForbiddenOrder and the forbidden-error-codes page wrap Check: go test ./internal/errcode/... passes with the NEW catalog entries and catalog test
- [ ] 3.3 Call the existing route ANY /api/server/caller/access with an access token in an e2e step against the real CLI and check the code member keeps HTTP 403 for tool refusals and 503 for the gate store fault Check: API description error list names the NEW codes; `make e2e` passes with tool refusals on 403 and gate store fault on 503. Confirmed by: CI (make e2e).
- [ ] 3.4 Wire the NEW codes, update the forbidden-error-codes docs page with entries for the NEW codes, and run the usability check Check: `make check` passes with zero findings. Confirmed by: CI (make check).
