## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Find the lease primitive and any caller of pruneExpired with rg, and record file:line in the pull request body. Write the failing tests TestRetentionSweepPrunesExpired and TestRetentionSweepSkipsWhenLeaseHeld. Check: go test ./pkg/dustpan -run TestRetentionSweep (NEW file pkg/dustpan/area_retention_test.go)
- [ ] 1.2 Write the failing test that the sweep audits each refusal and each failure of the prune call, because the port writes no denial entry. Check: go test ./pkg/dustpan -run TestRetentionSweepAuditsRefusalAndFailure (in pkg/dustpan/area_retention_test.go)
- [ ] 1.3 Write the failing test that the sweep derives its cutoff only from the retention setting and never from an input, and that the request type and the store refuse a cutoff after the current time. The effect line of the decision says a record is deleted only after the retention period passes, and a setting of `0` deletes nothing. Check: go test ./pkg/dustpan -run TestRetentionSweepCutoffFromSetting (in pkg/dustpan/area_retention_test.go)

## 2. Implementation

- [ ] 2.1 Add the sweeper type, its constructor and the lease use. Check: go test ./pkg/dustpan -run TestRetentionSweep (edits pkg/dustpan/area_retention.go)
- [ ] 2.2 Register the schedule trigger. Check: go test ./internal/cron (edits files in internal/cron)
- [ ] 2.3 Merge the check of an empty namespace or the server scope in pkg/store/internal/gatestore/records.go (line 316 at the review of the decision-record change) with the name parse beside it, or keep it with a comment that says it is a defense-in-depth layer and names the layers that it does not rely on. Check: the check shows once, or its comment names the other layers; go test ./pkg/dustpan -run TestRetentionSweep

## 3. Definition of done

- [ ] 3.1 Wire the sweep to the worker. No route changes. The e2e step starts two real server processes on one database, sets `decisionRecords.retentionHours` through PATCH /api/dustpan/v1/settings, and reads the audit entry of the sweep with an access token of a site administrator. Check: make e2e (NEW step in e2e/). Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Add the docs page for retention of decision records and check that its commands run (NEW file docs/guides/retention-of-decision-records.md). Check: make docs-lint
- [ ] 3.3 Run the full check. Check: make check. Confirmed by: CI (the agent runs the fast checks only).
