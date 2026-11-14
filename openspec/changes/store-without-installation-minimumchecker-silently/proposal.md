---
version: 1
---

Issue: #2183.

## Problem

An embedder builds a server with a third-party store and expects the binary minimum check to guard restore start, worker check and readiness, but a store without installation.MinimumChecker skips each check with no error.

## Evidence

- MinimumChecker is declared in pkg/installation and asserted on the restore worker path and in dustpan instances. Proof: rg MinimumChecker found pkg/installation/instances.go:98, pkg/backup/restore_steps.go:637 and pkg/dustpan/instances.go:112
- CheckBinaryMinimum has implementations in both SQL dialects. Proof: rg CheckBinaryMinimum found pkg/store/sqlite/restore_minimum.go:32 and pkg/store/postgres/restore_minimum.go:49
- NewServer, ErrMissingOption and newDeletedSweeper exist on main. Proof: rg NewServer found internal/servertest/servertest.go:477; rg ErrMissingOption found pkg/dustpan/sinks.go:90; rg newDeletedSweeper found pkg/dustpan/sweep.go:40
- The issue reports that no storetest case calls CheckBinaryMinimum and only internal adapter tests do. Proof: Issue #2183 body states no storetest case calls CheckBinaryMinimum and only internal adapter tests do.

## Fix boundary

- NewServer refuses a store that does not implement installation.MinimumChecker with an error that wraps ErrMissingOption.
- The same construction refusal applies to a nil maintenance service, to a store without the restore rows that the store check needs, and to a store without the purge records that the backlog gauge needs.
- storetest gains MinimumChecker conformance coverage for accept, refuse and read failure on both SQL adapters.

Out of scope:

- Change of the minimum-version rule itself
- New purge gauge semantics

## Check

- Add failing construction test for a store without MinimumChecker in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW test `TestNewServerRefusesStoreWithoutMinimumChecker` in NEW file pkg/dustpan/minimum_checker_test.go fails on main, passes after fix.
- Add failing construction test for a decorating wrapper that hides MinimumChecker in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW test `TestNewServerRefusesHidingWrapper` in NEW file pkg/dustpan/minimum_checker_test.go fails on main, passes after fix.
- Add failing construction tests for a nil maintenance service and for a store without store-check restore rows in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW tests `TestNewServerRefusesNilMaintenanceService` and `TestNewServerRefusesStoreWithoutRestoreRows` fail on main, pass after fix.
- Add failing construction test for a store without purge-record support for the backlog gauge in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW test `TestNewServerRefusesStoreWithoutPurgeRecords` fails on main, passes after fix.
- Add failing storetest conformance cases for CheckBinaryMinimum accept, refuse and read failure in NEW file pkg/store/storetest/minimum_checker_test.go. Check: NEW tests `TestMinimumCheckerAccept`, `TestMinimumCheckerRefuse` and `TestMinimumCheckerReadFailure` in NEW file pkg/store/storetest/minimum_checker_test.go fail on main, pass after fix on sqlite and postgres suite runs.
- Wire the MinimumChecker assertion into NewServer and the same refusal into the store-check and purge-gauge setup paths. Check: `go test ./pkg/dustpan ./pkg/store/... -count=1` passes.
- Update the embedder docs page for the required store interfaces, run the usability check from the docs page alone, and run `make check`. Check: `make check` passes with zero findings. Confirmed by: CI (make check). No route and no e2e step: construction-time change only. Wiring is NewServer refusal. Docs page is the embedder store-interface page. Usability check is the embedder startup path from the docs page alone.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- MinimumChecker declares CheckBinaryMinimum on main. (`pkg/installation/instances.go:98 (func checkInstanceField)`)
- Postgres store implements CheckBinaryMinimum on main. (`pkg/store/postgres/restore_minimum.go:54 (func Store.CheckBinaryMinimum)`)
- NewServer constructs the server on main. (`pkg/dustpan/dustpan.go:774 (func NewServer)`)
- newDeletedSweeper validates its store options on main. (`pkg/dustpan/sweep.go:40 (func newDeletedSweeper)`)
- ErrMissingOption exists on main. (`pkg/dustpan/dustpan.go:107`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 281: Any S3-compatible store: the database decides which write wins** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current), 8 (current).
- Changed by: ADR 340 changes decision 2. Chain to the end: ADR 340.
- Current rule: this ADR, with ADR 340.
- Quote: "A backlog gauge (owner). A metric gives the number of pending purge records and the age of the oldest one. An operator sees when deletes do not keep up."
- Quote: "A minimal start-up check, and a deep store check (owner)."
- Effect: Decision 5 keeps the purge backlog gauge visible. Decision 8 keeps the start-up check visible. The change refuses construction of a server whose store setup leaves either guard silent, instead of running without the gauge or without the minimum check.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Keep the silent skip for a store without MinimumChecker? No. The pack cites the no-magic and fail-closed rules, and ADR 281 decisions 5 and 8 require a visible gauge and a visible start-up check. A silent skip contradicts both, so construction refuses with the existing ErrMissingOption.
- Does the fix add a new store port method or a new error type? No new port method and no new error code. The fix wires the existing installation.MinimumChecker interface and the existing ErrMissingOption into NewServer validation and adds missing storetest coverage for CheckBinaryMinimum.
- How do decorating store wrappers keep MinimumChecker? Wrappers keep the interface by embedding the wrapped store or by forwarding CheckBinaryMinimum. The NewServer assertion tests the outer store value, so a wrapper that hides MinimumChecker is refused at construction like a store that lacks it. The per-call assertions stay fail-closed for a store swapped after construction.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
