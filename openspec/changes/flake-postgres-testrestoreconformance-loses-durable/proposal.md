---
version: 1
---

Issue: #2218.

## Problem

A developer runs the postgres store suite and hits a flake: the restore conformance test loses its durable claim under CPU load, so the run fails even though the code is correct.

## Evidence

- The runner defines a lease-deadline stop when the claim nears expiry. Proof: pkg/durable/runner.go:57:	// errLeaseDeadline stops the work when the claim comes near its expiry
- The postgres package has a restore conformance test. Proof: pkg/store/postgres/postgres_test.go:932
- The shared restore conformance entry point is RunRestore. Proof: pkg/store/storetest/restore.go:58 (func RunRestore)

## Fix boundary

- Give the restore conformance suite a fixed test-only lease of 10 minutes with 30 second renewal except the swap lock-conflict case, so one slow renewal under CPU load does not end the restore claim.

Out of scope:

- Changes to runner lease renewal or expiry behavior
- Skipping or quarantining the test

## Check

- Run the loaded case in pkg/store/postgres/postgres_test.go:932 on main with a CPU-heavy job beside it and record the lease failure Check: `go test -race -timeout 60m ./pkg/store/postgres/ -run TestRestoreConformance` fails with the lost claim text on main under load
- In the lease setup used by pkg/store/storetest/restore.go:58, set the lease to 10 minutes with 30 second renewal except the swap lock-conflict case in pkg/store/postgres/restore_swap_conflict_internal_test.go:36 which keeps the production lease Check: `go test -race -timeout 60m ./pkg/store/postgres/ -run TestRestoreConformance` passes alone
- Run pkg/store/postgres/postgres_test.go:932 five times beside a CPU-heavy job Check: five passes with DUSTPAN_TEST_POSTGRES_URL set
- Run make check for pkg/store/storetest/restore.go:58 Confirmed by: CI (make check) Check: `make check` with zero findings

Depends on:

- restore-loses-its-own-claim
- `restore-loses-its-own-claim` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The postgres package has a restore conformance test. (`pkg/store/postgres/postgres_test.go:932`)
- The shared restore conformance suite runs the restore cases. (`pkg/store/storetest/restore.go:58 (func RunRestore)`)
- Short-lease expiry is covered by the durable heartbeat suite. (`pkg/durable/durabletest/heartbeats.go:256 (func testShortLeaseExpiresFirst)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should the flaky test be skipped or quarantined? No. The issue forbids it: do not skip or quarantine the test.
- Should the runner lease behavior change? No. Runner renewal and expiry stay unchanged. Only the test setup lease duration changes.
- Does a longer restore-suite lease remove lease-expiry coverage? No. pkg/durable/durabletest/heartbeats.go:256 (func testShortLeaseExpiresFirst) covers short-lease expiry. The restore suite does not test the lease itself.
- Why not retry a claim lost for errLeaseDeadline? A retry would hide a real lost claim and change runner semantics. A longer test-only lease keeps the runner unchanged.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
