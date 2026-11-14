## 1. Failing tests

- [ ] 1.1 Add failing conformance test for sweep of expired state.write allow records. Check: TestStateWriteRecordPrunedAfterRetention fails on main: expired allow record still reads.
- [ ] 1.2 Add failing test that an allow with no gate still writes one decision record. Check: TestStateWriteNoGateStillRecords fails on main: allow with no gate writes no record.
- [ ] 1.3 Add failing test for sweep of expired refusal and unlocked-write records. Check: TestStateWriteRefusalPrunedAfterRetention fails on main: expired refusal record still reads.
- [ ] 1.4 Add failing test for bounded batches, restart mid-batch, and one lease holder. Check: TestStateWriteSweepSingleHolder fails on main: two sweepers delete the same batch.
- [ ] 1.5 Verify the TFE lock route guard order in code and add a failing held-lock test. Check: `rg -n 'asks the guard|guard.*lock|lock.*guard' internal/api/tfe` shows the held-lock guard order plus TestStateWriteHeldLockWritesNoRecord fails on main.

## 2. Build

- [ ] 2.1 Build the bounded retention sweep for state.write records ordered by record ID with the store lease, including the open-step and missing-entry guards. Check: `go test ./pkg/gate/... -run TestStateWriteRecord -count=1` passes.
- [ ] 2.2 Wire the sweep to the server worker as a system task and cover the lock retry path end to end. Check: `make e2e` OpenTofu lock retry with -lock-timeout=30s writes one record per free-lock attempt and held-lock retries write none.

## 3. Definition of done

- [ ] 3.1 Expose the wiring and the route with authorization actions. Check: Route declares its authorization actions and the object of each action.
- [ ] 3.2 Add the docs page, run the usability check from the docs page alone, and pass make check. Check: Docs page runs as written and `make check` reports zero findings.
