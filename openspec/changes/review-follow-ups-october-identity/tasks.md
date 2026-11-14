## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the NEW test TestRecordFailureStates in the NEW file internal/stepup/contract_test.go for missing, expired, and non-open challenges. Check: The NEW test `TestRecordFailureStates` fails on main via `go test ./internal/stepup/ -run TestRecordFailureStates -count=1`.
- [ ] 1.2 Write the NEW test TestCreateChallengeKinds in the NEW file internal/stepup/contract_test.go for session, access-token, empty, and unsupported kinds with nothing stored on refusal. Check: The NEW test `TestCreateChallengeKinds` fails on main via `go test ./internal/stepup/ -run TestCreateChallengeKinds -count=1`.
- [ ] 1.3 Write the NEW test TestStepupAuditLayer in the NEW file internal/stepup/contract_test.go for exactly one entry per refusal error. Check: The NEW test `TestStepupAuditLayer` fails on main via `go test ./internal/stepup/ -run TestStepupAuditLayer -count=1`.
- [ ] 1.4 Write the NEW tests TestTOTPReservation and TestTOTPConcurrentConsume in the NEW file internal/stepup/reservation_test.go for fabricated, reused, higher-step, and concurrent reservations. Check: The NEW tests `TestTOTPReservation` and `TestTOTPConcurrentConsume` fail on main via `go test ./internal/stepup/ -run TestTOTPReservation -count=1`.
- [ ] 1.5 Write the NEW tests TestKeyAddBinding and TestAtomicReset in the NEW file internal/stepup/reservation_test.go for reuse after revocation, failed enrollment, one reset entry, and injected transaction failure. Check: The NEW tests `TestKeyAddBinding` and `TestAtomicReset` fail on main via `go test ./internal/stepup/ -run TestKeyAddBinding -count=1`.
- [ ] 1.6 Write the NEW test TestCheckAccessPropagatesUnexpected in pkg/gate/service_test.go for an unexpected CheckAccess error with no added denial. Check: The NEW test `TestCheckAccessPropagatesUnexpected` fails on main via `go test ./pkg/gate/ -run TestCheckAccessPropagatesUnexpected -count=1`.
- [ ] 1.7 Split TestForbiddenOrder in internal/errcode/errcode_test.go into separate CSRF-versus-denied and other-site-versus-denied cases with no joint case. Check: `go test ./internal/errcode/ -run TestForbiddenOrder -count=1` passes after the split.

## 2. Fix

- [ ] 2.1 Fix the contracts in internal/stepup/stepup.go and pkg/gate/service.go to meet the requirements. Check: `go test ./internal/stepup/ ./internal/errcode/ ./pkg/gate/ -count=1` passes.
- [ ] 2.2 Edit docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md to assign the service to ErrNotFound, ErrExpired, and ErrUsed and the store to ErrMismatch and ErrFactorRefused. Check: The RFC section names the auditing layer of each of the five refusal errors.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the package tests and make check; this contract-only change has no user-visible path, BLOCKED by no user-visible route or gate. Confirmed by: CI (`make check`). Check: `make check` reports zero findings.
