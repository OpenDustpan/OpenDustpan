## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Regression tests and fixes

- [ ] 1.1 Add regression test in pkg/dustpan/apidesc_test.go that mounts a test-only adapter with an extra undescribed route through server assembly Check: New test TestAssembledServerReportsUndescribedRoute in pkg/dustpan/apidesc_test.go fails on main and passes after, plus positive described route passes
- [ ] 1.2 Add a concrete http.Handler field fixture in pkg/dustpan/apidesc_internal_test.go and extend the field discovery it uses with embedded traversal Check: New test for the concrete handler field in pkg/dustpan/apidesc_internal_test.go fails on main and passes after, existing interface-field checks still pass
- [ ] 1.3 Run body-declaration assertions in pkg/dustpan/apidesc_bodies_test.go against both every-module and dev-module servers Check: TestEveryOperationMarksItsBodies in pkg/dustpan/apidesc_bodies_test.go passes for both server configs and fails on a dev-only fixture gap
- [ ] 1.4 Instrument streamed-write test in pkg/workspace/nil_recorder_test.go for early recorder guard of WriteStreamed in pkg/workspace/write_state.go Check: Missing-recorder test in pkg/workspace/nil_recorder_test.go observes zero placeholder and zero attempt writes, and removal of only the early guard in pkg/workspace/write_state.go fails it
- [ ] 1.5 Preserve ErrNotFound in pkg/stack/updates.go when the stack is deleted between existence check and ListUpdates Check: New History race test for pkg/stack/updates.go expects ErrNotFound and empty history stays empty
- [ ] 1.6 Remove blob keys from OpenStateBlob and cleanup errors in pkg/workspace/service.go Check: NEW test in pkg/workspace/state_scope_test.go for four error paths asserts no key in errors with errors.Is matches over the full chain

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run affected package tests with no new routes Check: `go test -race ./pkg/dustpan/... ./pkg/stack/... ./pkg/workspace/...` passes
- [ ] 3.2 Run full checks and usability review of error text for key leakage. Confirmed by: CI (`make check`). Check: `make check` passes with zero findings
