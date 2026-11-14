---
version: 2
---

Issue: #1030.

## Why

**Persona: embedder.** An embedder builds a server that enforces blueprint quotas. Without a SQL quota counter the limits pass in memory tests but fail in a backed installation, and overuse goes past max.

The user path this change must make work:

1. **Server start.** Embedder opens a store transaction and uses the SQL quota.Counter inside it.
2. **API.** Concurrent submits enforce units and requests limits through quota.Tx in the guarded transaction.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Reserve or Record above max returns quota.ErrExceeded. | Quota exceeded for the key, with the key, the limit, and the usage. Lower usage or raise max, then submit again. | `DUSTPAN_TEST_POSTGRES_URL=<url> go test -race ./internal/quota/...` |
| Concurrent write conflict returns a retryable store error. | Store conflict during Reserve or Record, with the key. Run the guarded submit again in a new transaction. | `DUSTPAN_TEST_POSTGRES_URL=<url> go test -race ./internal/quota/...` |

Evidence for the claims above:

- The quota port defines ErrExceeded and quota.Tx with Reserve and Record. Proof: internal/quota/quota.go
- A conformance suite for quota.Counter adapters exists. Proof: internal/quota/quotatest/quotatest.go
- Issue 1030 asks for the SQL adapter of quota.Tx for SQLite and PostgreSQL with Reserve and Record in the same transaction as the guarded write. Proof: #1030

## What Changes

- SQL quota.Tx adapter for SQLite and PostgreSQL with Reserve, Record, SetEnd, release, expiry, and settle
- Conformance coverage for ErrExceeded, release, expiry, atomic Record, and concurrent Reserve
- Quota tables in both baselines, queries, backup list entry, and PostgreSQL test runs

Out of scope:

- Catalog routes and settings of limits
- Quota reconcile schedule and usage repair
- Expr quota rules
- Catalog request state of an expired reservation

## Usability

Where the path still costs the person time or surprises them:

- No user path changes; misuse shows as quota.ErrExceeded with the key, the limit, and the usage.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- quota.ErrExceeded is defined in the quota package (`internal/quota/quota.go:33`)
- quota.Counter and quota.Tx are defined in the counter file (`internal/quota/counter.go:86`)
- Conformance suite for quota.Counter adapters exists with Counter harness (`internal/quota/quotatest/quotatest.go:28`)
- Conformance harness runs Counter.InTx with a namespace fence (`internal/quota/quotatest/quotatest.go:146`)
- Self-service submit path references the quota.Tx of its own transaction (`internal/selfservice/request.go:144`)
- PostgreSQL test URL env name is DUSTPAN_TEST_POSTGRES_URL (`internal/servertest/servertest.go:15`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 115: Quotas and reservations** (not owner-tagged).

- Decisions that apply: 4 (current), 5 (current), 6 (current), 7 (current), 8 (current), 11 (current), 12 (current).
- Changed by: ADR 222 changes decision 2; ADR 222 supersedes decision 13; ADR 224 changes decision 2; ADR 224 supersedes decision 13; ADR 226 supersedes decision 13; ADR 241 extends; ADR 286 changes. Chain to the end: ADR 222, ADR 224, ADR 226, ADR 241, ADR 286, ADR 335, ADR 351, ADR 354, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 222, 224, 226, 241, 286.
- Quote: "A reservation holds a unit for a create in flight."
- Quote: "A reservation ends in one of two ways."
- Quote: "A reservation ends at the deadline of its revision."
- Quote: "`Reserve(ctx, reservation, checks)`: decision 5."
- Effect: The change implements the SQL adapter of the port that decision 12 defines, with Reserve and Record in the same transaction that decision 5 requires, with the release, settle, deadline, destroy, and lower-limit rules that decisions 6, 7, 8, and 11 require, and with the settled plus reserved usage that decision 4 requires.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Fixed controls stay fixed (owner)."
- Effect: Quota reservations and counts stay fixed controls outside gates, so the SQL adapter enforces limits directly and does not add a gate decision.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does Record store a partial submit when one check fails? ADR 115 decision 12 answers it: Record checks the requests limits with the submit counted and stores one submit for the key of each check only when each one passes, and returns quota.ErrExceeded otherwise.
- Does Reserve run in the same transaction as the guarded write? ADR 115 decision 5 answers it: the reservation is written through quota.Tx in the transaction of the submit, with the fence checked in the same transaction.
- What ends a reservation and does expiry count toward usage? ADR 115 decisions 6 and 7 answer it: release deletes the reservation and settle moves it to settled, both idempotent, and each reservation ends at the deadline of its revision and expired rows do not count.
- What happens when the guarded transaction rolls back after Reserve? ADR 115 decisions 5 and 6 answer it: a rolled-back transaction stores nothing, so a crash between Reserve and commit leaves no count.
- How do Reserve and Record count settled units? ADR 115 decision 4 answers it: usage is the settled count plus the reserved count; the SQL adapter reads both from the quota table in the same transaction, while the hourly inventory reconcile of decision 9 stays out of scope.
- What happens on a lower max and on a destroy? ADR 115 decision 11 answers it: a lower max refuses new creates and changes no row; ADR 115 decision 8 answers it: a destroy lowers the settled count when its revision ends applied.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
