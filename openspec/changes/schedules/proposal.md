---
version: 1
---

Roadmap: `docs/roadmap.md:472` (Schedules).

## Why

**Persona: operator.** An Operator runs two or more serve instances of one installation behind one load balancer and needs each background tick to run at most once, so a deploy never doubles work.

The user path this change must make work:

1. **Server start.** Start three opendustpan serve instances on one PostgreSQL database and one S3 bucket behind the round-robin proxy.
2. **API.** Set each job schedule to a 10 s interval through the NEW test-only constructor in the test build.
3. **E2e.** Run 60 s and read durable_operations start keys through ANY /api/server/instances with an access token to check at most 1 row per key.
4. **Docs page.** Read docs/guides/high-availability.md for the supported layout and the measured figures.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| An API write fails while PostgreSQL is stopped. | 503 with a Retry-After header in the shape of the API. The tick stays due. | `curl -i $BASE/health/ready` |

Evidence for the claims above:

- Main stores durable operations in durable_operations and has a durable package with a schedule test. Proof: pkg/backup/backup.go:51 TableDurableOperations; pkg/durable/durabletest/durabletest_test.go:35 TestMemSchedules
- Main lists durable operations on PostgreSQL. Proof: pkg/store/postgres/durable.go:44 (func Store.DurableOperations)
- Main has an instance list route and an HA guide. Proof: ANY /api/server/instances in the route table; docs/guides/high-availability.md

## What Changes

- Add a NEW test-only 10 s schedule interval value that production builds refuse.
- Add the exit-test step: run 60 s on three instances and count durable_operations start keys.
- Document the measured tick figures with the date and no SLA.

Out of scope:

- Replication between installations, promotion and forwarding.
- Active-active across sites and SQLite with more than one instance.
- New schedule kinds.
- Kill takeover of a tick operation beyond the existing lease path.
- 503 retry behavior of each CLI.

Depends on:

- alternation
- `alternation` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A skipped slot after a crash looks like a missing tick. The guide explains it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The schedule suite covers the claim. (`pkg/durable/durabletest/durabletest_test.go:35 (func TestMemSchedules)`)
- The PostgreSQL store lists durable operations. (`pkg/store/postgres/durable.go:44 (func Store.DurableOperations)`)
- A restore keeps durable operations. (`pkg/store/storetest/durable_restore.go:20 (func testRestoreKeepsDurableOperations)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A schedule is a row. A periodic job that needs one runner per installation is a schedule row with `next_run_at`. One conditional `UPDATE` claims a due row and moves `next_run_at` forward."
- Quote: "Each claimed operation has an expiry on the database clock."
- Quote: "Each claim has a fencing token that increases on each takeover."
- Quote: "There is no instance registry."
- Effect: The step checks the existing schedule claim and the existing operation lease. It adds no new claim path and no new expiry.
- Conflict: none.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (extended), 6 (current), 8 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "The lease stays at 30 seconds (owner). The lease period of a durable operation stays fixed at 30 seconds. No flag and no setting changes it."
- Quote: "When the database is not available, each tool-compatible API answers 503 with a `Retry-After` header, not 500."
- Quote: "The docs give the recovery figures that the exit test of milestone 7 measures."
- Effect: Three instances run active-active on one database. A 503 with Retry-After covers a down store. Docs give measured figures with date and no SLA. The 10 s test value changes only the schedule interval. The 30 s lease stays fixed.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Leases and durable operations across the gap."
- Effect: A database gap shorter than the lease resumes by renewal. A longer gap hands the tick operation over after expiry. The 60 s count checks at-most-once across the gap.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How do two instances avoid running the same tick twice? ADR 72 decision 3: one conditional UPDATE claims a due schedule row and moves next_run_at forward. No leader election and no instance registry per ADR 72 decision 2.
- Which claim expires after 30 s? ADR 72 decision 2: the lease with expiry and fencing token sits on the tick durable operation, not on the schedule row. The schedule row has no expiry. ADR 280 decision 8: the lease stays at 30 s.
- Does the start key hold the holder instance? No. Start keys stay deterministic with no instance ID per ADR 72 decision 2 and ADR 166 decision 7. The count checks one row per start key in durable_operations.
- How does the test set a 10 s interval without a new flag? The interval is a NEW test-only constructor value, not a stored setting and not a flag. A production build refuses it. ADR 280 decision 8 forbids only a lease flag.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
