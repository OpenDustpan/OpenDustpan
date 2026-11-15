---
version: 1
---

Issue: #3129.

Short form: bug (test harness). Owner lane: M8.

## Problem

**Persona: maintainer.** the test TestMonitorWindowOnPostgres
(load/scale_run_internal_test.go:105) can fail with `dbTransactionsPerSec`
named unmeasured. A load report can lose its transaction rate for the same
reason. The failure depends on the timing of one query, not on the code
under test.

## Evidence

- The monitor has one connection (`newMonitor`, load/scale.go:701,
  `pgx.ConnectConfig`). `begin` (load/scale.go:793) starts a sampler that
  runs `SELECT count(*) FROM pg_stat_activity ...` on that connection every
  `sampleEvery` (500 ms, load/scale.go:51), with `sampleCtx`.
- `end` (load/scale.go:826) calls `m.cancel()`, waits for the sampler, and
  then reads the transactions on the same connection (`transactions`,
  load/scale.go:780).
- pgx v5.11.0 (go.mod:13) builds each connection with
  `DeadlineContextWatcherHandler` (pgconn/config.go:472 of the module): a
  canceled context during a query sets a deadline on the network
  connection, the read fails, and pgconn closes the connection.
- So when `m.cancel()` arrives while a sampler query runs, the transaction
  read fails, `txOK` is false, and `transactionRate` (load/scale.go:908)
  answers nil. A loaded machine (a local `make check`) makes the sampler
  query slower, so the cancel hits it more often. The test passed with
  `-race -count=5` on an idle machine.

## Fix boundary

- The sampler stops between queries, never during one: `begin` gives the
  loop a stop channel, the loop checks it at each tick, and each sampler
  query runs with the context of `begin` (not a context that `end`
  cancels). `end` closes the stop channel, waits for the loop, then reads
  the transactions. A query in flight finishes first, so the connection
  stays open.
- No change to the window, the sample period, the report or the test's
  expectations. The test is not skipped.

## Check

`DUSTPAN_TEST_POSTGRES_URL=<url> go test ./load/ -run 'TestMonitorWindowOnPostgres|TestMonitorEndDuringSample' -race -count=20`
passes.

## ADR check

No ADR is touched: the change fixes the sampler of the load harness.
`rg -n "load harness|sampler" docs/adr` finds no decision on how the sampler
stops.

## Open questions

none. Considered: a second connection for the transaction read (refused:
it doubles the connections of the monitor and hides the race, which still
closes the sampler connection); a retry of the read (refused: it hides the
cause).
