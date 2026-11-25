## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

Scenario "Retry after restart" passes on main already (the key is in the job row). Its test in task 4.2 is a regression test, not a failing test. Each other scenario maps to a test below that fails on main.

- [x] 1.1 Write table tests in the NEW file `pkg/idempotency/retention_test.go`: func TestNewRetention (23 hours is refused with `ErrInvalid`, 24 and 48 hours are accepted), func TestRetentionExpired (an age of exactly 24 hours is not expired, 24 hours and 1 millisecond is expired), and func TestRetentionZeroValue (`Retention{}.Expired` returns `ErrInvalid`). Check: go test ./pkg/idempotency -run 'TestNewRetention|TestRetentionExpired|TestRetentionZeroValue'
- [x] 1.2 Add the NEW optional field `AgeJob func(t *testing.T, id job.ID, by time.Duration)` to `jobtest.Harness` in `pkg/job/jobtest/jobtest.go`. It moves the creation time of a stored job back by `by`. A harness that leaves it nil skips the cases that need it. Add the cases in the NEW file `pkg/job/jobtest/key_retention.go` and list them in func Run: "a key of a job of exactly the retention age replays", "a key of a job older than the retention starts a new job that holds the key", "another request under an expired key starts a new job", "a retry after the new job replays the new job", and "a failed start under an expired key keeps the old key" (use a busy unit). Check: go test ./pkg/job/jobtest
- [x] 1.3 Set the NEW field `AgeJob` in the harness of func TestJobConformance in `internal/store/sqlite/jobs_test.go` and `internal/store/postgres/jobs_test.go`. It runs an UPDATE of `created_at_ms` in the test file only. Check: go test ./internal/store/sqlite -run TestJobConformance -timeout 60m
- [x] 1.4 Write func TestCreateKeyRetention in the NEW file `internal/store/jobstore/key_retention_test.go`, over the fakeTx of `internal/store/jobstore/updatetime_paths_test.go` (extend fakeTx with the methods that func Create reaches). Cases: a store built with `idempotency.Retention{}` refuses a start whose key names a job and stores no job; a clear that finds a newer version makes func Create run one more attempt, which replays the new job. Check: go test ./internal/store/jobstore -run TestCreateKeyRetention
- [x] 1.5 Write func TestUpdateJobClearsKeyOnly in `internal/store/sqlite/jobs_index_internal_test.go` and `internal/store/postgres/jobs_index_internal_test.go`: an update with an empty key clears the stored key, and an update with another key that is not empty keeps the stored key. Check: go test ./internal/store/sqlite -run TestUpdateJobClearsKeyOnly

## 2. Implementation

- [x] 2.1 Add the NEW Retention type, `MinRetention`, func NewRetention, func JobStartRetention and func (Retention) Expired in the NEW file `pkg/idempotency/retention.go`, as the design says. Check: go test ./pkg/idempotency
- [ ] 2.2 Change the `UpdateJob` query in `internal/store/postgres/queries.sql` and `internal/store/sqlite/queries.sql` to clear the key only (design decision 4), and run `make generate` for `internal/store/postgres/internal/db` and `internal/store/sqlite/internal/db`. Pass `Row.Key` in func updateJobRow of `internal/store/postgres/state_close.go` and `internal/store/sqlite/state_close.go`. Check: sqlc diff, and go test ./internal/store/sqlite -run 'TestUpdateJobClearsKeyOnly|TestJobRowIsWrittenOnlyByUpdateJobRow'
- [x] 2.3 In `internal/store/jobstore/store.go`, keep the Retention in `Store` (func New takes it), read `tx.Now` before func sameKey, and in func sameKey clear the key of an expired job through func update (design decisions 2, 3 and 5). Update the port doc of `Create` in `pkg/job/job.go`. Check: go test ./internal/store/jobstore

## 3. Side effects (computed from main)

- [ ] 3.1 Not applicable: the change adds and changes no request flow. The start route sends the same queries for a replay. Only a start under an expired key sends one more update, and no flow in (read only) `internal/servertest/` sends an Idempotency-Key (a search for "Idempotency-Key" in that directory on main gives no line). So its round-trip budgets stay. Check: none.

## 4. Definition of done

- [x] 4.1 Wire the retention into the server: func Jobs in `internal/store/postgres/jobs.go` and `internal/store/sqlite/jobs.go` pass the NEW `idempotency.JobStartRetention()` to func New. Check: go test ./internal/store/sqlite -run TestJobConformance -timeout 60m, and go test ./pkg/job -run TestStartIdempotency
- [ ] 4.2 Add func TestJobStartIdempotencyAfterRestart in the NEW file `e2e/job_start_idempotency_test.go`. It starts a job with POST /api/dustpan/v1/namespaces/{namespace}/jobs, an access token and an Idempotency-Key, stops the real server binary with func (*Env) KillServer of the package e2e, starts it again with func (*Env) StartServer (like the restart in `e2e/crash_test.go`), and retries the same request with the same key. The retry answers the same job ID and `Idempotent-Replayed: true`. Set up the workspace and the pool like (read only) `e2e/jobs_api_test.go`. A comment in the file names only tests that exist in the pull request. Check: make e2e
- [x] 4.3 Write the NEW file `docs/reference/job-start-idempotency.md` with the section "Idempotency-Key retention": a key names its job for 24 hours from the start of the job; after that, the same key starts a new job; how to find the first job with GET /api/dustpan/v1/namespaces/{namespace}/jobs. Check: make docs-lint
- [x] 4.4 In `docs/reference/jobs-api.md`, section "Retry a start", replace "The server keeps a key as long as it keeps the job." with the 24-hour rule and a link to `job-start-idempotency.md`. Check: make docs-lint
- [x] 4.5 Check that the agent completes the retry path from the docs page. Check: manual usability check of the docs page, recorded in the pull request body.
- [ ] 4.6 Run all checks. Check: make check. Confirmed by: CI (the agent runs the fast checks only).
