---
version: 1
---

Issue: #1219.

## Problem

A developer starts a job for a unit that was just removed and gets a store-internal error or a driver error that errors.Is and errors.As cannot match, so retry and alert logic cannot act on it.

## Evidence

- jobstore defines ErrUnitNotInInventory as a store-internal constant Proof: pkg/store/internal/jobstore/tx.go:34
- The shared Apply boundary is Store.Apply Proof: pkg/store/internal/jobstore/apply.go:17 (func Store.Apply)
- pkg/job already defines job.ErrUnitGone Proof: pkg/job/job.go:88
- jobtest already asserts job.ErrUnitGone with errors.Is on a different path Proof: pkg/job/jobtest/unitid.go:123:	if !errors.Is(err, job.ErrUnitGone) {
- jobIndexError exists in the SQLite adapter Proof: pkg/store/sqlite/jobs.go:183:func jobIndexError(what string, err error) error {
- jobIndexError exists in the PostgreSQL adapter Proof: pkg/store/postgres/jobs.go:162 (func jobIndexError)

## Fix boundary

- Wrap the missing-unit case of job.Store.Apply in job.ErrUnitGone and assert it with errors.Is.
- Stop jobIndexError from exposing driver errors by formatting the driver part with %v.
- Add a jobtest case with 16 parallel creates in one namespace that checks completeness and unique seq.

Out of scope:

- Production dispatcher wiring for job.Store.Apply
- Registry, drift, chat, and other backlog items

## Check

- Extend the case at pkg/job/jobtest/unitid.go:102 (func testUnitGone) with failing assertions for Apply for gone unit, Missing-unit on memory store, Missing-unit on SQLite store, Missing-unit on PostgreSQL store, and Same name made again against pkg/store/internal/jobstore/apply.go:17. Check: `go test ./pkg/job/jobtest/ -count=1` fails on main then passes
- At pkg/store/sqlite/jobs.go:183 (func jobIndexError) add failing hostile checks for Unknown index error hides driver on SQLite and Driver text stays diagnosable by calling jobIndexError directly with an unknown violation. Check: `go test ./pkg/store/sqlite/ -count=1` fails on main then passes
- At pkg/store/postgres/jobs.go:162 (func jobIndexError) add a failing hostile check for Unknown index error hides driver on PostgreSQL by calling jobIndexError directly with an unknown violation. Check: `go test ./pkg/store/postgres/ -count=1` fails on main then passes where a database is available
- Write the NEW file `pkg/job/jobtest/concurrency.go` with failing cases for Sixteen parallel creates, Seq collision retries, and Unit removed during concurrent create. Check: `go test ./pkg/job/jobtest/ -count=1` fails on main then passes
- In pkg/store/internal/jobstore/apply.go:17 (func Store.Apply) wrap jobstore.ErrUnitNotInInventory in job.ErrUnitGone for both the pre-check miss and a concurrent insert conflict, and document the case on the port in pkg/job/store.go. Check: `go test ./pkg/store/sqlite/ -count=1` and the postgres suite where available pass
- In pkg/store/sqlite/jobs.go:183 (func jobIndexError) and pkg/store/postgres/jobs.go:162 (func jobIndexError) change the default branch to format the driver part with %v while keeping the driver text in the message. Check: `go test ./pkg/store/sqlite/ ./pkg/store/postgres/ -count=1` passes where a database is available
- Run the NEW case in `pkg/job/jobtest/concurrency.go` and the gone-unit assertions in pkg/job/jobtest/unitid.go on the memory, SQLite, and PostgreSQL stores. Check: `go test ./pkg/job/... ./pkg/store/sqlite/ -count=1` and the postgres suite where available pass
- Run the job and store suites across memory, SQLite, and PostgreSQL stores. Check: `go test ./pkg/job/... ./pkg/store/sqlite/ -count=1` passes and the postgres suite passes where available

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- job.Store.Apply exists as the job write path (`pkg/store/internal/jobstore/apply.go:17 (func Store.Apply)`)
- pkg/job defines job.ErrNotFound (`pkg/job/job.go:45 (var job.ErrNotFound)`)
- pkg/job defines job.ErrUnitGone (`pkg/job/job.go:88 (var job.ErrUnitGone)`)
- jobstore defines ErrUnitNotInInventory (`pkg/store/internal/jobstore/tx.go:34 (const jobstore.ErrUnitNotInInventory)`)
- jobIndexError maps index violations in the PostgreSQL job store (`pkg/store/postgres/jobs.go:162 (func jobIndexError)`)
- jobIndexError maps index violations in the SQLite job store (`pkg/store/sqlite/jobs.go:183 (func jobIndexError)`)
- jobtest already covers a unit-gone case on a different path (`pkg/job/jobtest/unitid.go:102 (func testUnitGone)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which job sentinel should Apply return for a missing unit? Use the existing sentinel at pkg/job/job.go:88 because the pack proves job.ErrUnitGone exists and pkg/job/jobtest/unitid.go:102 already uses errors.Is for it. The missing part is the Apply path at pkg/store/internal/jobstore/apply.go:17, which does not map the store-internal constant at pkg/store/internal/jobstore/tx.go:34. No new sentinel is needed.
- How to stop driver errors from escaping jobIndexError? The pack proves jobIndexError exists at pkg/store/sqlite/jobs.go:183 and pkg/store/postgres/jobs.go:162. Change the default branch on both dialects to format the driver value with %v while keeping its text in the message, so errors.As cannot reach a driver type.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
