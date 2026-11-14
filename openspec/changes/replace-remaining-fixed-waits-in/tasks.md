## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing tests TestPollsPassUnderLoad, TestPollTimeoutNamesWaitAndState, TestPollRetriesTransientNotReady, TestPollFailsFastOnClosedDatabase and TestPollStopsAtCancellation in NEW `pkg/store/storetest/poll_test.go`. Check: `go test ./pkg/store/storetest/ -run 'TestPoll'` fails on main before the fix.

## 2. Fix waits

- [ ] 2.1 Replace waitForDropSchema, waitForSwap, staleFinish, gateWait, and lockWaitLimit in pkg/store/storetest with poll loops on state change that end at the shared per-test deadline. Check: `rg -n 'waitForDropSchema|waitForSwap|staleFinish|gateWait|lockWaitLimit' pkg/store/storetest` shows each helper polls with the shared deadline; no fixed 10 second, 30 second, or 1 minute wait remains.
- [ ] 2.2 Share one per-test deadline in checkRecovered in pkg/store/storetest so waitBackupEnd plus the operation loop end once and stop at test cancellation. Check: `rg -n 'newTestDeadline|testDeadline' pkg/store/storetest` shows one deadline passed into waitBackupEnd and the operation loop.
- [ ] 2.3 Document StuckTestLimit in pkg/store/storetest as test-only pending the owner export decision. Check: `rg -n 'StuckTestLimit' pkg/store/storetest` shows the doc comment.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the store conformance tests in pkg/store/storetest under load several times beside another package. Check: `go test -race -count=3 ./pkg/store/storetest/ -timeout 60m` passes beside `go test ./pkg/backup/`, with every old assertion still running.
- [ ] 4.2 Run full checks and confirm every assertion from the old waits in pkg/store/storetest still runs. Check: `make check` passes with zero findings. Confirmed by: CI (make check).
