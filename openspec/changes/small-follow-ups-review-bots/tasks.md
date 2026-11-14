## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a test in NEW pkg/secret/open_clear_test.go: close without read fires the clear hook once. Check: TestOpenContentCloseWithoutReadClearsKey fails on main, passes after the fix.
- [ ] 1.2 Add a test in NEW pkg/secret/open_clear_test.go: close twice is idempotent, close after full read clears, and read after close fails closed. Check: TestOpenContentCloseTwiceAndReadAfterCloseFailClosed fails on main, passes after the fix.
- [ ] 1.3 Add a test in NEW pkg/secret/open_clear_test.go: cancel the open context without close clears the key, including during a read. Check: TestOpenContentContextCancelWithoutCloseClearsKey fails on main, passes after the fix.
- [ ] 1.4 Add a test in NEW pkg/secret/open_clear_test.go: a truncated header clears the key. Check: TestOpenContentHeaderFailureClearsKey fails on main, passes after the fix.
- [ ] 1.5 Add a test in NEW pkg/secret/open_clear_test.go: concurrent Close and Read clear once with no race. Check: TestOpenContentConcurrentCloseAndRead does not report a data race on main with -race semantics and passes after the fix.
- [ ] 1.6 Add a test in NEW pkg/job/jobtest/recorder_clock_test.go: a fixed clock gives exact record times. Check: TestRecorderHarnessFixedClockSetsExactRecordTimes fails on main, passes after the fix.

## 2. Fix

- [ ] 2.1 Clear the key on Close, failure and context end in pkg/secret/stream.go and pkg/secret/itemsealer.go. Check: go test ./pkg/secret/... passes.
- [ ] 2.2 Add the injectable Now field in pkg/job/jobtest/recorder.go and pkg/job/jobtest/recorder_keytx.go. Check: go test ./pkg/job/jobtest/... passes with exact time assertions.

## 3. Definition of done

- [ ] 3.1 Run the race tests on both packages. BLOCKED by no request path. Confirmed by: CI (go test -race). Check: go test -race ./pkg/secret/... ./pkg/job/... passes.
- [ ] 3.2 Run all checks. Confirmed by: CI (make check). Check: make check reports zero findings.
