## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test in NEW file `internal/api/tofuhttp/state_write_logging_test.go` for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token. It forces the 500 with a fault-injecting workspace store and checks one problem-path line with cause and request id. Check: NEW file internal/api/tofuhttp/state_write_logging_test.go: NEW test TestStateWrite500LogNamesCauseAndRequestID fails on main, then passes after the fix.
- [ ] 1.2 Write the failing redaction test in NEW file `internal/api/tofuhttp/state_write_logging_test.go` for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token. It checks redaction of driver detail and the 500 character cut. Check: NEW file internal/api/tofuhttp/state_write_logging_test.go: NEW test TestStateWrite500LogRedactsAndCutsText fails on main, then passes after the fix.

## 2. Fix

- [ ] 2.1 Edit file `internal/api/tofuhttp/state_write.go` for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token: add the redacted wrapped cause and the request id to the one problem-path fault-log line for the state POST, with no second line and no body change. Check: NEW file internal/api/tofuhttp/state_write_logging_test.go: TestStateWrite500LogNamesCauseAndRequestID passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run `make check` with zero findings for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token. No docs page changes, because no user-visible API changes. Confirmed by: CI (make check). Check: `make check` passes with zero findings, plus existing route `POST /tofu/state/{namespace}/{workspace}` called with an access token in the tests above passes.
