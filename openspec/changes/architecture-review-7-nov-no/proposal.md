---
version: 1
---

Issue: #2575.

## Problem

Time injection is ad hoc per package and never reaches lease, claim, or adapter paths, so lease tests use real time and flake while audit times can split across layers.

## Evidence

- Production code still calls the wall clock directly, including the durable lease runner. Proof: Pack code_facts show rg time.Now matches 408 and first lines pkg/durable/runner.go:658 ticker and pkg/durable/runner.go:663 deadline timer, and pack paths show pkg/durable/runner.go exists.
- Backup and audit paths also use the wall clock on main. Proof: Pack paths show pkg/backup/service.go exists and pkg/audit/ship.go exists; pack code_facts show pkg/backup/service.go:333 timer and pkg/audit/ship.go shipInterval.

## Fix boundary

- Add shared clock with Clock, Real, NewTimer, NewTicker, and Fake with Advance
- Convert pkg/durable lease and claim paths to the shared clock
- Add forbidigo bans with a to-convert exclusion list and write the conversion recipe

Out of scope:

- Conversion of packages beyond pkg/durable
- Audit timestamp move for unconverted HTTP adapters
- Follow-up per-package conversions tracked in issue 2575

## Check

- Write failing tests for the NEW Fake Advance, Reset and Stop scenarios and for the NEW TestLeaseMargin in pkg/durable Check: `go test ./internal/clock/... ./internal/clocktest/...` on the NEW packages fails on main and the NEW TestLeaseMargin in pkg/durable fails on main
- Add NEW internal/clock with Clock and Real, and NEW internal/clocktest with Fake and Advance Check: `go test ./internal/clock/... ./internal/clocktest/...` on the NEW packages passes
- Convert pkg/durable runner lease, claim time and deadline timers to the shared clock Check: `rg -n time.Now pkg/durable` on the converted pkg/durable returns nothing except clock wiring
- Add forbidigo bans for direct wall-clock calls in pkg and internal except the NEW clock packages, cmd and listed exclusions Check: `make lint` passes and a trial direct call fails forbidigo in a converted package
- Write the NEW conversion recipe docs page for embedders Check: `make docs-lint` passes for the NEW conversion recipe page
- Wire clock.Real{} in pkg/dustpan and run the full check with usability from the recipe alone. Confirmed by: the driver's gates. Check: `make check` has zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The audit shipper computes ship intervals from the wall clock. (`pkg/audit/ship.go:411 (func shipInterval)`)
- A durable runner claim test exists on main. (`pkg/durable/runner_claimed_test.go:16`)
- The dustpan wiring creates the backup service on main. (`pkg/dustpan/area_backup.go:108 (func newBackupService)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR in the pack require or forbid a shared clock package? Pack lists no ADRs or RFCs, so no quoted decision applies to this change.
- Should this change convert all wall-clock callers at once? No. The issue scopes this change to the NEW clock packages plus pkg/durable as reference; other callers stay on a to-convert exclusion list that only becomes shorter.
- Does this change need user-facing behavior specs? No user-visible behavior changes; only internal lease timing moves to the injected clock.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
