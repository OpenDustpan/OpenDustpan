---
version: 2
---

Issue: #717.

## Why

**Persona: operator.** An Operator runs two or more instances on one database. The Operator needs audit entries that never overwrite each other and lease, session, and sign-in expiry that agrees on each instance, even when process clocks differ.

The user path this change must make work:

1. **Server start.** Start two instances on one PostgreSQL database behind one load balancer.
2. **API.** Run the tool path through the proxy and confirm each instance serves audit writes with an access token.
3. **Docs page.** Read the several-instances guide for the clock and chain behavior.
4. **Console.** Check the chain and the expiry from either instance during the run.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Two instances append to one audit scope at once and one write loses. | 409 with the error shape of the API and the new head; send again with the new head | `curl /api/audit/check` |
| The database does not answer and the instance cannot read the shared clock. | 503 with `Retry-After: 5` in the shape of its API | `curl /health/ready` |

Evidence for the claims above:

- The restore service file exists and this part leaves its staging and preserve logic unchanged. Proof: Path docs/guides/backup-restore-upgrade.md exists, docs/guides/several-instances.md exists, pkg/backup/restore_service.go exists.
- No in-process restore guards remain on main under the old names. Proof: rg failStoppedRestores gives 0 matches; rg fenceStoppedRestore gives 0 matches; rg inflight gives 0 matches; rg runningElsewhere gives 0 matches; rg yieldToEarlierRunner gives 0 matches; rg recreateAfterSwap gives 0 matches.
- The failover bump and the preserve path remain on main. Proof: rg BumpFailoverVersions gives 22 matches with first lines pkg/backup/restore_service.go:685 and pkg/backup/restore_record.go:131; rg PreserveAfterRestore gives 3 matches with first line pkg/backup/service.go:516.
- Main already has chain concurrency tests and a database clock test. Proof: Main lists pkg/audit/audittest/chain.go:615 (func testChainConcurrentEnds) and pkg/store/storetest/attempt_deadline.go:286 (func testDatabaseClock).

## What Changes

- Audit chain append checks the expected head in the same transaction and reports a mismatch with the new head.
- Lease, session, and sign-in expiry read the shared database clock in the same statement.
- Sign-in failure budget counts each failure once in the shared database window.

Out of scope:

- Durable restore steps, staging schema name in the row, fenced swap and FinishRestore token check
- Sink lag and readiness gate
- Write-path installation check
- Drop of the old live schema in its own step
- Swap time measurement in the 2-instance smoke test

Depends on:

- `track-medium-low-items-several-durable-fenced-restore` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Two instances that race an audit append show a mismatch error; the caller reads the new head and sends again.
- Clock answers come from the database, so local log times differ from expiry answers by skew.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the durable claim step fence type. (`pkg/durable/durable.go:150 (func Claim.Step)`)
- Main has a database clock port and a test for it. (`pkg/blob/attempt.go:75 (func CheckAttempt)`)
- Main has a database clock test in the store suite. (`pkg/store/storetest/attempt_deadline.go:286 (func testDatabaseClock)`)
- Main keeps restore swap helpers in the postgres store. (`pkg/store/postgres/restore.go:391 (func keepControlTables)`)
- Main keeps the one-running guard for restores in the baseline of each dialect. (`pkg/store/postgres/migrations/0168_baseline.sql:2407`)
- Main keeps chain append concurrency tests in the audit suite. (`pkg/audit/audittest/chain.go:615 (func testChainConcurrentEnds)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (changed), 8 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock. The worker that runs it extends the expiry. Each claim has a fencing token that increases on each takeover."
- Quote: "A shared sign-in failure budget. The failure budget is a windowed counter in the database, shared by all instances. Each failed attempt writes once. A successful attempt writes nothing."
- Effect: Follows the lease on the database clock, the fixed hash field list, and the shared sign-in budget. Audit append adds a compare-and-swap on the stored head with no change to the hash input. Expiry reads the database clock in the same statement.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "The fence stays. Each write in a namespace carries a `namespace.Fence`, and the store checks it in the same transaction, as ADR 4 decides."
- Effect: Audit append in a namespace carries the namespace fence and checks it in the same transaction as the head check.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Requests fail fast with 503. During the gap, each request that needs the database gets 503 with `Retry-After: 5`, in the shape of its API (W3, ADR 280 decision 4). The server does not retry a request on its own. A second retry layer would h"
- Effect: A database gap that stops the shared clock read fails fast with 503 and Retry-After 5 in the shape of each API.
- Conflict: none.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "Scope: one installation, active-active (owner). Milestone 7 makes one installation run active-active: two or more `opendustpan serve` instances behind one load balancer, on one PostgreSQL database and one S3-compatible object store."
- Effect: Applies active-active on one database to audit append and expiry with no instance registry and no process memory guard.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Can two instances append to one audit chain or renew one lease at the same time? ADR 72 decision 2 answers it: expiry is on the database clock, the claim has a fencing token, and every step write, renewal and completion checks the token in its own transaction.
- What does a successful sign-in write to the shared budget? ADR 72 decision 8 answers it: the budget is a windowed counter in the database, each failed attempt writes once, and a successful attempt writes nothing.
- Does this part change restore start, the failover bump, or the staging schema name? No. The pack shows zero matches for failStoppedRestores, fenceStoppedRestore, inflight, runningElsewhere, yieldToEarlierRunner and recreateAfterSwap. This part leaves BumpFailoverVersions and PreserveAfterRestore unchanged and leaves the durable restore steps to the first part.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
