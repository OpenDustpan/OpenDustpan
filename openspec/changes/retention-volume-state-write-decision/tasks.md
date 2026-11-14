## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing test for sweep of expired state.write allow records. Check: TestStateWriteRecordPrunedAfterRetention fails on main: expired allow record still reads.
- [ ] 1.2 Add test that an allow with no gate still writes one record. Check: TestStateWriteNoGateStillRecords passes on main and guards the keep behavior.
- [ ] 1.3 Add failing test for sweep of expired refusal and unlocked-write records. Check: TestStateWriteRefusalPrunedAfterRetention fails on main.
- [ ] 1.4 Add failing test for bounded batches, restart mid-batch and one lease holder. Check: TestStateWriteSweepSingleHolder fails on main.

## 2. Build

- [ ] 2.1 Build pruneExpired with batches of 100, the 5 minute lease, hourly cadence, stuck-step rule and one-transaction batch plus prune entry. Check: `go test ./pkg/gate/... -run TestStateWriteRecord -count=1` passes.
- [ ] 2.2 Wire the sweep to the server worker as a system task. Check: BLOCKED by the missing sweep route: no existing route triggers it. Check with `go test ./pkg/dustpan/... -run TestStateWriteSweepWired -count=1`.

## 3. Definition of done

- [ ] 3.1 Expose the wiring and show the effective sweep values in the settings resource. Check: GET /api/dustpan/v1/settings with an installation administrator access token shows cadence, batch size and last prune count.
- [ ] 3.2 Add an e2e step: OpenTofu lock retry with -lock-timeout=30s against the real CLI. Check: `make e2e` shows held-lock retries write no record.
- [ ] 3.3 Add the docs page, run the usability check from the docs page alone, and pass make check. Check: `make check` reports zero findings.
