## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for 4xx answers with no hint, and 500, 503 and connection failure with the hint, in NEW internal/cli/hint_test.go. Check: TestCliNoRetryHint
- [ ] 1.2 Write failing tests for the runner-off JSON 404 with an access token, in NEW internal/api/runnerapi/off_test.go. Check: TestRunnerApiOff
- [ ] 1.3 Write a failing faultlog test for ENOSPC to no_space in NEW internal/api/faultlog/cause_test.go. Check: TestFaultCauseNoSpace

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the CLI hint helper, the runner-off handler and the faultlog order. Confirmed by: CI (make check). Check: TestCliNoRetryHint
- [ ] 3.2 Call GET /api/runners/{namespace}/pools with an access token on a server with no public URL. Confirmed by: the driver's gates. Check: TestRunnerApiOff
- [ ] 3.3 Run the real CLI namespace create for an existing name with an access token. BLOCKED by the namespace create route. A person's step. Check: `opendustpan namespace create`
- [ ] 3.4 Update the docs page for the CLI hint rule and the no_space cause, and run `make check` with zero findings. Confirmed by: CI (make check). Check: `make check`
