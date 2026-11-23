## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write a test in `pkg/idempotency/idempotency_test.go` that a retry after a store reopen gets the stored answer. Check: go test ./pkg/idempotency -run TestRetryAfterReopenReplays
- [ ] 1.2 Write a test in `pkg/idempotency/idempotency_test.go` that a record expires after 24 hours. Check: go test ./pkg/idempotency -run TestRecordExpiresAfter24h

## 2. Implementation

- [ ] 2.1 Add the NEW Retention type and its NEW constructor in the NEW file `pkg/idempotency/retention.go`. Check: go test ./pkg/idempotency
- [ ] 2.2 Extend the existing file `internal/store/codec/idempotency.go` in place to store the expiry in the codec, and run the conformance suite. Check: go test ./pkg/store/... -timeout 60m

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the retention into the server for the job start. Check: go test ./pkg/job -run TestStartIdempotency
- [ ] 4.2 Add an e2e step in the NEW file `e2e/job_start_idempotency_test.go` that starts a job with POST /api/dustpan/v1/namespaces/{namespace}/jobs and an access token of a service principal, restarts the real server binary, and retries the same request with the same Idempotency-Key. Check: make e2e.
- [ ] 4.3 Update the retention time in the NEW section "Idempotency-Key retention" of the NEW file `docs/reference/job-start-idempotency.md`. Check: make docs-lint
- [ ] 4.4 Check that the agent completes the retry path from the docs page. Check: manual usability check of the docs page.
- [ ] 4.5 Run all checks. Check: make check.
