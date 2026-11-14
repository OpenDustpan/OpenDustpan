## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add TestRetryAfterFailedSeedWrite in internal/clientgen/conformance/limits_test.go for a failed seed write that retries after recovery (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestRetryAfterFailedSeedWrite)) Check: go test -race ./internal/clientgen/conformance/ -run TestRetryAfterFailedSeedWrite
- [ ] 1.2 Add TestReseedOnReusedAddress in internal/clientgen/conformance/limits_test.go for a new server on a reused address that reseeds (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestReseedOnReusedAddress)) Check: go test -race ./internal/clientgen/conformance/ -run TestReseedOnReusedAddress
- [ ] 1.3 Add TestSuccessSeededOncePerInstance in internal/clientgen/conformance/limits_test.go for one post per live server instance (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestSuccessSeededOncePerInstance)) Check: go test -race ./internal/clientgen/conformance/ -run TestSuccessSeededOncePerInstance
- [ ] 1.4 Add TestConcurrentSeedSharesInFlight in internal/clientgen/conformance/limits_test.go for shared in-flight write, joint retry, conflict success, and close during write (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestConcurrentSeedSharesInFlight)) Check: go test -race ./internal/clientgen/conformance/ -run TestConcurrentSeedSharesInFlight

## 2. Fix

- [ ] 2.1 Change the fallback seed cache in internal/clientgen/conformance/fixtures.go to key by server instance, skip storage of failed writes, and delete the entry on close (Confirmed by: CI (go test -race ./internal/clientgen/conformance/)) Check: go test -race ./internal/clientgen/conformance/

## 3. Definition of done

- [ ] 3.1 Run the full check for internal/clientgen/conformance/fixtures.go and the new tests (Confirmed by: CI (`make check`)) Check: `make check` with zero findings
