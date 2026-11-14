---
version: 1
---

Issue: #2027, #2033, #2040, #2347.

## Problem

An Operator runs several serve instances behind one load balancer so the Pulumi CLI and OpenTofu keep working during instance loss and rolling deploys. The 4 extra database trips on OpenTofu lock and unlock add one network hop per trip on a remote database.

## Evidence

- An OpenTofu lock costs 45 DB trips and an unlock 11 after 842af92, up from 42 and 10. Proof: Issue 2347 numbers table: lock 42 to 45, unlock 10 to 11.
- Two gate reads that start with GetInventoryUnit and one UpdateInventoryLastRelease statement per lock save cause the rise. Proof: Issue 2347 bisect items 1 and 2.
- Main measures per-request round trips in TestRoundTripsOfRequests. Proof: internal/servertest/roundtrips_test.go:935 (func TestRoundTripsOfRequests)

## Fix boundary

- Pass the stable unit ID from the lock handler into both gate reads so the two GetInventoryUnit trips go; a stale ID answers 404 workspace.not-found and writes no gate row.
- Fold the last-release write into the lock save and the unlock save as one statement per save on the workspace row, so lock and unlock each lose one trip while the 24-hour match keeps working.
- Lower the round-trip budgets in the same change.

Out of scope:

- W9 PostgreSQL failover readiness stays in 2027
- W11 HA layout docs and W12 sizing stay in 2027
- ADR 281 any-store work of 2033: blob port contract, unique keys, probe, reapers, paged List, gauge, quota
- Flows of 2040 beyond OpenTofu lock and unlock
- Changing owner-tagged words of any ADR

## Check

- Edit `internal/servertest/roundtrips_test.go:935` to set failing budgets lock 42 and unlock 10; BLOCKED by TFE workspace lock route Check: `go test ./internal/servertest -run TestRoundTripsOfRequests -count 1` fails on main with lock 45 and unlock 11; BLOCKED by TFE workspace lock route
- Edit `pkg/store/postgres/workspaces.go:1513` and `pkg/store/postgres/internal/db/queries.sql.go:7136` to pass the stable unit ID to both ListGateObjectsOfScope reads and fold the last-release write into the lock and unlock saves in both dialects; BLOCKED by TFE workspace lock route Check: `go test ./internal/servertest -run TestRoundTripsOfRequests -count 8` passes; BLOCKED by TFE workspace lock route
- Run `e2e/ha_test.go:38` with the real Pulumi CLI and OpenTofu, then run make check; BLOCKED by TFE workspace lock route Check: `E2E_INSTANCES=3 make e2e` passes on `e2e/ha_test.go:38` plus `make check` with zero findings; BLOCKED by TFE workspace lock route

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main measures per-request DB round trips in one test. (`internal/servertest/roundtrips_test.go:935 (func TestRoundTripsOfRequests)`)
- Main reads an inventory unit ID by namespace, tool and name. (`pkg/store/postgres/internal/db/queries.sql.go:7136 (func Queries.GetInventoryUnit)`)
- Main lists gate objects of a scope. (`pkg/store/postgres/internal/db/queries.sql.go:14093 (func Queries.ListGateObjectsOfScope)`)
- Main writes the last release on each lock save. (`pkg/store/postgres/internal/db/queries.sql.go:22850 (func Queries.UpdateInventoryLastRelease)`)
- Main has a multi-instance HA e2e suite. (`e2e/ha_test.go:38 (func TestHighAvailability)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 5 (extended).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "two or more `opendustpan serve` instances behind one load balancer, on one PostgreSQL database and one S3-compatible object store."
- Effect: Keeps the one-database active-active scope; only trips fall.
- Conflict: none.

**ADR 337: The last released lock is in the workspace API and not in the inventory** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The record is still kept on the workspace row. It still matches for 24 hours after the release."
- Effect: The fold writes the workspace row and keeps the 24-hour match.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 2 (current), 8 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Requests fail fast with 503. During the gap, each request that needs the database gets 503 with `Retry-After: 5`, in the shape of its API (W3, ADR 280 decision 4)."
- Effect: The folded write rolls back whole on a broken connection; the server does not resend.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the last-release record live after the fix? ADR 337 decision 2: on the workspace row, 24-hour match.
- What happens on a database gap? ADR 298 decision 2: 503 with Retry-After: 5, no server retry.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
