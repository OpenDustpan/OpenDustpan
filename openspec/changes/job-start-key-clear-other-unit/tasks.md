## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main (after `job-start-idempotency-retention-24h` is on main), and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add the NEW case func testKeyExpiredOtherUnit in `pkg/job/jobtest/key_retention.go`: start on unit A with a key, age the job by 24 hours and 1 millisecond, start on unit B with the same key, and check that the job on unit B holds the key, the job on unit A holds none, and one entry `job:key-expired` names both units. It fails on main because the entry has no `newUnit` detail (NEW symbol `newUnit`). Check: go test ./internal/store/sqlite -run TestJobConformance -timeout 60m
- [ ] 1.2 Add the NEW case func testKeyExpiredOtherPrincipal in `pkg/job/jobtest/key_retention.go`: another principal with the same key string clears nothing, and the first job keeps its key and its version. Check: go test ./pkg/job -run TestStartIdempotency
- [ ] 1.3 Add the NEW case func testKeyOtherUnitWithin24Hours in `pkg/job/jobtest/key_retention.go`: the same key on unit B within 24 hours gives ErrKeyConflict and no job. Check: go test ./internal/store/sqlite -run TestJobConformance -timeout 60m

## 2. Implementation

- [ ] 2.1 In func clearExpiredKey of `internal/store/jobstore/store.go`, add the detail `newUnit` to the entry of the clear. Check: go test ./internal/store/jobstore

## 3. Side effects (computed from main)

- [ ] 3.1 Not applicable: the change adds no request flow and no route. The start route sends the same queries.

## 4. Definition of done

- [ ] 4.1 In `docs/reference/job-start-idempotency.md`, add the section "A key that names a job on another unit": the rule, the entry `job:key-expired` with both units, and why no check on the old unit is needed. Check: make docs-lint
- [ ] 4.2 Run all checks. Check: make check. Confirmed by: CI (the agent runs the fast checks only).
