## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add a failing server test in the NEW file internal/servertest/pgdown_test.go that stops PostgreSQL and calls the existing route `GET /api/dustpan/v1/maintenance` with credential kind `access token` and passes its existing authorization gate, and expects namespace.unavailable with Retry-After. It covers the NEW mapStoreError. Declaration and adapter files: internal/api/problem/problem.go and internal/api/server. Check: TestPgDownReadAnswersUnavailable. Confirmed by: CI (make test).
- [ ] 1.2 Add a failing e2e step in the NEW file e2e/pgdown_test.go that calls the existing route `ANY /api/inventory/unit-collection` with credential kind `access token` and passes its existing authorization gate, using the real Pulumi CLI for Pulumi state routes and the real OpenTofu CLI for TFE state routes, and checks the 503 contract shapes. Check: TestPgDownToolReadsAnswerUnavailable. Confirmed by: CI (make e2e).
- [ ] 1.3 Add a failing worker test in the NEW file pkg/job/jobtest/pgdown_test.go for the NEW logCauseClass: claim and schedule failures log database-unavailable, and a hostile error omits host, DSN and credential. Check: TestPgDownWorkerLogNamesCauseClass. Confirmed by: CI (make test).

## 2. Fix

- [ ] 2.1 Add the NEW func CauseClass in the NEW file internal/errcode/cause_class.go with the allowlist, and map it to namespace.unavailable in internal/api/problem/problem.go near line 373. Check: TestPgDownReadAnswersUnavailable. Confirmed by: CI (make test).
- [ ] 2.2 Route worker claim and schedule store errors through CauseClass in the NEW file pkg/job/cause_log.go. Check: TestPgDownWorkerLogNamesCauseClass. Confirmed by: CI (make test).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the mapping on the existing route ANY /api/inventory/unit-collection with an access token, authorization actions unchanged, with a harness PostgreSQL stop and restart and recovery without a server restart. Check: `make e2e` PostgreSQL stop and restart step. Confirmed by: CI (make e2e).
- [ ] 4.2 Update the docs error-code entry for namespace.unavailable with the cause, the Retry-After hint and the one command. Check: `make docs-lint`. Confirmed by: CI (make docs-lint).
- [ ] 4.3 Run the usability check from the docs page and the full checks. Check: `make check` with zero findings. Confirmed by: the driver's gates.
