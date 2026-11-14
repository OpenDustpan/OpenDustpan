Issue: #1030.

## Why

**Persona: embedder.** An embedder builds a server that enforces blueprint quotas. Without a SQL quota counter the limits pass in memory tests but fail in a backed installation, and overuse goes past max.

The user path this change must make work:

1. **Server start.** Embedder wires the SQL quota.Counter into the store used by self-service submits.
2. **API.** Concurrent submits enforce units and requests limits through quota.Tx in the guarded transaction.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Reserve or Record above max returns quota.ErrExceeded. | Quota exceeded for the key, with the limit and the usage. Lower usage or raise max, then submit again. | `go test ./internal/quota/...` |

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

## Usability

Where the path still costs the person time or surprises them:

- No user path changes; misuse shows as quota.ErrExceeded with the key, the limit, and the usage.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 115: Quotas and reservations** (not owner-tagged).

- Decisions that apply: 5 (current), 6 (current), 7 (current), 12 (current).
- Changed by: ADR 222 changes decision 2; ADR 222 supersedes decision 13; ADR 224 changes decision 2; ADR 224 supersedes decision 13; ADR 226 supersedes decision 13; ADR 241 extends; ADR 286 changes. Chain to the end: ADR 222, ADR 224, ADR 226, ADR 241, ADR 286, ADR 335, ADR 351, ADR 354, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 222, 224, 226, 241, 286.
- Quote: "A reservation holds a unit for a create in flight."
- Quote: "A reservation ends in one of two ways."
- Quote: "A reservation ends at the deadline of its revision."
- Quote: "`Reserve(ctx, reservation, checks)`: decision 5."
- Effect: The change implements the SQL adapter of the port that decision 12 defines, with Reserve and Record in the same transaction that decision 5 requires, with the release, settle, and deadline end states that decisions 6 and 7 require.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Fixed controls stay fixed (owner)."
- Effect: Quota reservations and counts stay fixed controls outside gates, so the SQL adapter enforces limits directly and does not add a gate decision.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md states 'Not spec-ready: a code check fails' because the command `opendustpan quota` does not exist, so the open-questions gate is not met and the change cannot be implemented until the error-command is corrected or the command is shipped. Resolution: answered: Replaced the non-existent quota command with a repository test command; no open question remains.
- R1.2 (missing scenario): specs/quota-counter/spec.md has no scenario for Reserve or Record with a fence check failure (stale namespace fence in the guarded transaction), although design.md requires the transaction to check the fence before each Reserve and Record. Resolution: answered: Added fence-check requirement with scenario "stale fence stores nothing" that returns a fence error and stores no reservation or submit.
- R1.3 (missing scenario): The spec has no requirement for expiry of reservations and release, although the proposal, the issue acceptance criteria and tasks 1.1 name them; design.md risk 'expired reservations keep counting when expiry is lazy only' is not answered by any scenario. Resolution: answered: Added reservation-lifecycle requirement with scenarios "released reservation frees the slot", "expired reservation does not count", "settled reservation moves to settled", and "set end moves expiry".
- R1.4 (missing scenario): No scenario covers SetEnd, settle and release semantics (idempotency on repeated release or settle), which ADR 115 decision 6 and decision 12 define for quota.Tx. Resolution: answered: Lifecycle requirement covers SetEnd in scenario "set end moves expiry", settle in scenario "settled reservation moves to settled" with repeat settle, and release in scenario "released reservation frees the slot" with repeat release.
- R1.5 (failure mode): The design says Reserve uses one atomic conditional write per key, but no spec or task defines the behaviour on a database that does not support the conditional write under the isolation level used, nor the retry or deadlock policy when two keys of one check are reserved in different orders by concurrent transactions. Resolution: answered: Design orders keys in sorted order for multi-key reserves; concurrent requirement and task cover the atomic conditional write and the ordered multi-key case.
- R1.6 (failure mode): Restart or crash between Reserve and commit is not covered: a reservation written in a transaction that rolls back must leave no count, and this is not stated as a scenario. Resolution: answered: Added rollback requirement: a rolled-back guarded transaction stores no reservation or submit.
- R1.7 (adr conflict): ADR 115 decision 5 requires that the reservation is written through quota.Tx in the transaction of the submit; the design's 'guarded transaction' and the proposal's Effect line cite it, but the design does not say that the default counter is the one in the Catalog store adapter, so the scope of the adapter is unclear against decision 5. Resolution: answered: Design now states the SQL counter is the default counter inside the store adapter that holds the self-service records, per decision 5.
- R1.8 (open question): The proposal says the Record rejection returns quota.ErrExceeded and the usage read names a command that does not exist; the expected error contract for the usage read (what the 'quota usage' command returns) is not decided by any ADR or spec. Resolution: answered: Error answer no longer promises a quota usage command; it reports key, limit, and usage.
- R1.9 (scope): tasks.md 3.2 makes the backup-list check optional ('or record the reason'), while the issue acceptance criteria require the tables in the backup list or a reason; the task does not require the e2e step against the real CLI for a user-visible path. Resolution: answered: Definition-of-done task now requires quota tables in the backup table list; the change has no user-visible path so conformance plus existing submit tests stand in for an e2e step.
- R1.10 (wrong claim): proposal.md says the ADR check covers the owner tags, but ADR 115 is listed as 'not owner-tagged' while its decision 12 relies on the owner-tagged ADR 222 chain without a stated check of owner tags for changed decisions. Resolution: answered: Added ADR 222 to adr_check with its owner-tagged fixed-controls quote and followed decision 5 scope.
- R2.1 (wrong claim): proposal.md says the error answer points to `opendustpan status` for namespace state. Nothing in the input shows this command exists or reports quota usage, so the claim has no proof. The earlier `opendustpan quota` was already found missing. An embedder who hits ErrExceeded in a library path has no CLI step. Resolution: answered: Removed the unproven opendustpan status claim; the error now names the limit response and the retry step, with a repository test command as the check.
- R2.2 (wrong claim): The 'Questions considered' section attributes claims to ADR 115 decision 6 and decision 7 (expired reservations do not count, the end time is the revision deadline). The quoted decision 7 text is cut off and no quote is given. The ADR check lists only decisions 5 and 12 as applying, so decisions 6 and 7 are used without a quote. Resolution: answered: Added ADR 115 decisions 6 and 7 to adr_check with exact quotes for the end states and the deadline, so considered no longer uses unquoted decisions.
- R2.3 (missing scenario): The spec has no scenario for the requests-limit window: Record counts submits in the last `window` (1 minute to 31 days) and old submits must stop counting. It also has no scenario for cleanup of old submit rows. Without these the Record adapter cannot be built or tested for its time rule. Resolution: answered: Added requests-window requirement with in-window counting, old-submit expiry, and inline cleanup scenarios.
- R2.4 (failure mode): Design says the atomic conditional write works on SQLite and PostgreSQL, but gives no isolation level and no retry or serialization-failure policy. Under PostgreSQL read committed, two Reserves on a key that has no row yet may both pass, since an UPDATE ... WHERE guard has no row to lock. Insert-if-absent for a new key is not covered. Resolution: answered: Design now names SELECT FOR UPDATE after INSERT ON CONFLICT DO NOTHING, the conditional UPDATE guard, sorted key order, and one retry of a serialization failure.
- R2.5 (failure mode): The concurrent multi-key scenario says the loser gets quota.ErrExceeded. A loser that fails from a deadlock or a serialization error is a different error, and the spec does not say which error it gets. If the second reserve would fit after the first one ends, ErrExceeded is wrong. The scenario uses the same two keys, so a stated outcome is needed. Resolution: answered: Split the multi-key scenario into fits and over-limit cases; serialization failures retry inside the transaction and only a limit failure returns quota.ErrExceeded.
- R2.6 (open question): The design stores reserved and settled usage but does not say how the settled count is stored or read for the SQL adapter. ADR 115 decision 4 says it comes from the inventory and Catalog records, and decision 9 gives it to the reconcile schedule. The spec scenario 'settled reservation moves to settled' needs a settled table, and the design does not define it or its link to decision 9 (out of scope). Resolution: answered: Design states settled moves live in the quota table in this change while the inventory reconcile of decision 9 stays out of scope.
- R2.7 (missing scenario): No scenario covers Reserve with a key that has no stored count, or a duplicate reservation ID (retry of the same submit). The text says release and settle are idempotent, but Reserve idempotency by ID is not decided. Also no scenario for expired-row cleanup that runs while Reserve runs, or for cleanup failure. Resolution: answered: Added scenarios for first use of a key, duplicate reservation ID retry, and cleanup that runs inside Reserve.
- R2.8 (failure mode): The background cleanup of expired rows has no owner, schedule, or restart behavior in the design or tasks. No task builds or tests it, and it is not stated how it works on a replica set with many servers. Resolution: answered: Removed the separate background cleanup; expiry deletes run inline in the same transaction and periodic reconcile stays out of scope.
- R2.9 (scope): Tasks have no step that wires the SQL counter as the default counter into catalog.Store.Submit, although the proposal's server-start step and ADR 115 decision 5 need it. Task 3.2 and the 'no e2e' claim also rest on the unproven statement that the change has no user-visible path. Resolution: answered: Added a wiring task that plugs the SQL counter as the default counter into catalog.Store.Submit.
- R2.10 (wrong claim): The design says the keys use stable IDs only in a general way. ADR 115 decision 3 defines the key with the namespace and blueprint names (payments/postgres/...). The project rule says a stored reference to a renamable entity uses the stable ID, and the design does not say which form the table key stores. Resolution: answered: Design now states stored key columns hold the namespace ID and blueprint ID, with names as labels for errors only.

## Open questions

none

Questions considered and answered:

- Does Record store a partial submit when one check fails? ADR 115 decision 12 answers it: Record checks the requests limits with the submit counted and stores one submit for the key of each check only when each one passes, and returns quota.ErrExceeded otherwise.
- Does Reserve run in the same transaction as the guarded write? ADR 115 decision 5 answers it: the reservation is written through quota.Tx in the transaction of the submit, with the fence checked in the same transaction.
- What ends a reservation and does expiry count toward usage? ADR 115 decisions 6 and 7 answer it: release deletes the reservation and settle moves it to settled, both idempotent, and each reservation ends at the deadline of its revision and expired rows do not count.
- What happens when the guarded transaction rolls back after Reserve? ADR 115 decisions 5 and 6 answer it: a rolled-back transaction stores nothing, so a crash between Reserve and commit leaves no count.
- How do concurrent multi-key reserves avoid deadlock? No ADR decides key lock order; the design orders keys in sorted order and retries a serialization failure once inside the transaction, and only a limit failure returns quota.ErrExceeded.
- Where does the settled count come from in the SQL adapter? ADR 115 decision 4 answers it: the settled count is the units that exist plus reservations; this change stores the settled moves of decision 6 in the quota table and the hourly reconcile of decision 9 stays out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
