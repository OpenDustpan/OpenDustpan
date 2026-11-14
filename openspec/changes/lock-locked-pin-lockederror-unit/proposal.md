---
version: 1
---

Issue: #1857.

## Problem

A blank LockedError.Unit survives tests at six producers and turns the 409 lock.locked answer into a 500 server.internal answer.

## Evidence

- The answer builder for manual-lock refusals lives in internal/api/refusal. Proof: internal/api/refusal/locked.go:17
- refusal.Locked is called from the trigger fire path. Proof: internal/api/triggers/triggers.go:277
- Restore under a namespace lock is refused by refuseRestoreUnderLock in the SQLite store. Proof: pkg/store/sqlite/lifecycle.go:715
- Restore under a namespace lock is refused by refuseRestoreUnderLock in the PostgreSQL store. Proof: pkg/store/postgres/lifecycle.go:696 (func refuseRestoreUnderLock)
- The trigger fire route checks manual locks through checkManualLock. Proof: pkg/store/internal/jobstore/rules.go:101 (func checkManualLock)

## Fix boundary

- Add a refused-unit (tool, namespace, name) assertion at each of the six producers named in the issue.
- Use distinct locked and refused objects in the Pulumi and trigger answer tests so a swap of the two fails.

Out of scope:

- Changing the lock.locked answer shape or error code
- Adding production validation beyond the test pin

## Check

- Add refused-unit rows for SQLite refuseRestoreUnderLock, PostgreSQL refuseRestoreUnderLock, and stack restore under a namespace lock in the NEW file pkg/store/storetest/refused_unit_test.go. Check: `go test ./pkg/store/storetest/ -run TestRefusedUnit -count=1` passes on main and fails when Unit is blanked.
- Add refused-unit rows for workspace create and workspace restore under a namespace lock in the NEW file internal/api/tfe/refused_unit_test.go with an access token. Check: `go test ./internal/api/tfe/ -run TestRefusedUnit -count=1` passes on main and fails when Unit is blanked.
- Add a refused-unit row for checkManualLock through the trigger fire route in the NEW file internal/api/triggers/refused_unit_test.go with an access token. Check: `go test ./internal/api/triggers/ -run TestRefusedUnit -count=1` passes on main and fails when Unit is blanked.
- Use distinct locked and refused objects in the Pulumi answer test and the trigger answer test in NEW files internal/api/pulumi/locked_swap_test.go and internal/api/triggers/locked_swap_test.go. Check: `go test ./internal/api/triggers/ ./internal/api/pulumi/ -run TestLockedSwap -count=1` fails when locked and refused are swapped.
- Run the affected packages and the full gate; no production code changes. Confirmed by: CI (`make check`). Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- LockedError carries the refused unit. (`pkg/manuallock/manuallock.go:222 (func SafeText)`)
- refusal.Locked builds the lock.locked answer. (`internal/api/refusal/locked.go:27 (func Locked)`)
- The lock.locked error code is catalogued. (`internal/errcode/catalog_lock.go:13`)
- Restore under lock is refused in the PostgreSQL store. (`pkg/store/postgres/lifecycle.go:696 (func refuseRestoreUnderLock)`)
- The trigger fire route checks manual locks. (`pkg/store/internal/jobstore/rules.go:101 (func checkManualLock)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR govern LockedError.Unit coverage? No ADR in the pack governs LockedError.Unit; the change adds only test rows that pin the refused unit.
- Is any producer or answer shape still undecided? No. The pack proves refusal.Locked, refuseRestoreUnderLock, and checkManualLock locations, and the issue names the six producers; no open decision remains.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
