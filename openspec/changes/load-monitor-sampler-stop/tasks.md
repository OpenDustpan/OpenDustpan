## 1. Failing test

- [x] 1.1 No route: the test drives the monitor of the load harness only. Add the NEW test `TestMonitorEndDuringSample` in load/scale_run_internal_test.go: it makes the sampler query slow (a sampler query hook of the test, or `pg_sleep` through it), calls `end` while that query runs, and checks that `DBTransactionsPerSec` is measured. Check: `DUSTPAN_TEST_POSTGRES_URL=<url> go test ./load/ -run TestMonitorEndDuringSample -count=1` fails on main, because `end` (load/scale.go:826) cancels the sampler context during the query.

## 2. Fix

- [x] 2.1 No route: the change edits the sampler only. In `begin` (load/scale.go:793), replace the cancel of `sampleCtx` with a stop channel that the loop checks at each tick, and run each sampler query with the context of `begin`; in `end` (load/scale.go:826), close the stop channel and wait for the loop before the transaction read. Check: `DUSTPAN_TEST_POSTGRES_URL=<url> go test ./load/ -run 'TestMonitorWindowOnPostgres|TestMonitorEndDuringSample' -race -count=20` passes.

## 3. Definition of done

- [ ] 3.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
