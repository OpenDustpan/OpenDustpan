## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Extend the case at pkg/job/jobtest/unitid.go:102 (func testUnitGone) with failing assertions for Apply for gone unit, Missing-unit on memory store, Missing-unit on SQLite store, Missing-unit on PostgreSQL store, and Same name made again against pkg/store/internal/jobstore/apply.go:17. Check: `go test ./pkg/job/jobtest/ -count=1` fails on main then passes
- [ ] 1.2 At pkg/store/sqlite/jobs.go:183 (func jobIndexError) add failing hostile checks for Unknown index error hides driver on SQLite and Driver text stays diagnosable by calling jobIndexError directly with an unknown violation. Check: `go test ./pkg/store/sqlite/ -count=1` fails on main then passes
- [ ] 1.3 At pkg/store/postgres/jobs.go:162 (func jobIndexError) add a failing hostile check for Unknown index error hides driver on PostgreSQL by calling jobIndexError directly with an unknown violation. Check: `go test ./pkg/store/postgres/ -count=1` fails on main then passes where a database is available
- [ ] 1.4 Write the NEW file `pkg/job/jobtest/concurrency.go` with failing cases for Sixteen parallel creates, Seq collision retries, and Unit removed during concurrent create. Check: `go test ./pkg/job/jobtest/ -count=1` fails on main then passes

## 2. Fix

- [ ] 2.1 In pkg/store/internal/jobstore/apply.go:17 (func Store.Apply) wrap jobstore.ErrUnitNotInInventory in job.ErrUnitGone for both the pre-check miss and a concurrent insert conflict, and document the case on the port in pkg/job/store.go. Check: `go test ./pkg/store/sqlite/ -count=1` and the postgres suite where available pass
- [ ] 2.2 In pkg/store/sqlite/jobs.go:183 (func jobIndexError) and pkg/store/postgres/jobs.go:162 (func jobIndexError) change the default branch to format the driver part with %v while keeping the driver text in the message. Check: `go test ./pkg/store/sqlite/ ./pkg/store/postgres/ -count=1` passes where a database is available
- [ ] 2.3 Run the NEW case in `pkg/job/jobtest/concurrency.go` and the gone-unit assertions in pkg/job/jobtest/unitid.go on the memory, SQLite, and PostgreSQL stores. Check: `go test ./pkg/job/... ./pkg/store/sqlite/ -count=1` and the postgres suite where available pass

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the job and store suites across memory, SQLite, and PostgreSQL stores. Check: `go test ./pkg/job/... ./pkg/store/sqlite/ -count=1` passes and the postgres suite passes where available
- [ ] 4.2 Update the job store docs page in the same pull request. Check: `make docs-lint` passes
- [ ] 4.3 Run the full check suite and record zero findings. Check: `make check` with zero findings
