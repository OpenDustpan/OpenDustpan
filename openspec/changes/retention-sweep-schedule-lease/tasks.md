## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Find the lease primitive and any caller of pruneExpired with rg, and record file:line in the pull request body. Write the failing tests TestRetentionSweepPrunesExpired and TestRetentionSweepSkipsWhenLeaseHeld. Check: go test ./pkg/dustpan -run TestRetentionSweep (NEW file pkg/dustpan/area_retention_test.go)

## 2. Implementation

- [ ] 2.1 Add the sweeper type, its constructor and the lease use. Check: go test ./pkg/dustpan -run TestRetentionSweep (edits pkg/dustpan/area_retention.go)
- [ ] 2.2 Register the schedule trigger. Check: go test ./internal/cron (edits files in internal/cron)

## 3. Definition of done

- [ ] 3.1 Wire the sweep to the worker. No route changes. The e2e step starts two real server processes on one database and reads GET /api/dustpan/v1/settings with an access token of a site administrator. Check: make e2e (NEW step in e2e/)
- [ ] 3.2 Add the docs page for retention of decision records and check that its commands run. Check: make docs-lint
- [ ] 3.3 Run the full check. Check: make check
