---
version: 1
---

Short form: tech debt. It covers #3112.

## Problem

**Persona: maintainer.** The write-ahead log guard of the SQLite store reads
package variables. SetWALGuardForTest replaces them for the whole test
binary, so one tuning test changes the guard, and the log counts, of every
other store that the binary opens at that time. The tests of the store then
fail or pass by order, and a reader of one test cannot see what changes its
store.

## Evidence

- Package variables: `walGuardInterval` (pkg/store/sqlite/sqlite.go:101),
  `walGuardThresholdBytes` (:109), `walGuardBackoffCap` (:122),
  `walGuardWarnAfter` (:190), with `walGuardMu` (:198), and the tick
  collection `walGuardTicksMu`, `walGuardTicks`, `walGuardCollect`
  (:147-161).
- SetWALGuardForTest (pkg/store/sqlite/export_internal_test.go:34) swaps the
  four settings under `walGuardMu` and holds `walHookMu` until its restore
  runs; `pkg/store/sqlite/wal_test.go` calls it at :54, :339, :688, :738 and
  :786.
- The store already takes options: `type Option` (pkg/store/sqlite/sqlite.go:297),
  for example `WithLogger` (:303).

## Fix boundary

- The guard settings and the tick collection become unexported fields of
  `Store`, set by `Open` with the values of today.
- An unexported option of the store sets them for one store; the existing
  file `pkg/store/sqlite/export_internal_test.go` exports it to the tests. The
  WAL tests open their own store with it.
- Remove the package variables, `walGuardMu`, `walHookMu` and
  SetWALGuardForTest. WALGuardTicksForTest reads the ticks of one store.
- No behavior change in production: the defaults stay.

## Check

`rg -n '^var walGuard' pkg/store/sqlite` finds no line, `rg -n
SetWALGuardForTest pkg` finds no line, and `go test -race ./pkg/store/sqlite/...`
passes.

## ADR check

**ADR 364: Unexported fields and golangci-lint before added checks**
(owner-tagged, decision 1; Changed by: none).
- Quote: "This decision adds no custom analyzer and no new tool."
- Effect: the fix is structural (fields of the store set by its constructor),
  not a new check or a new lint rule.

## Open questions

none
