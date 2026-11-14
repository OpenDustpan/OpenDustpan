Issue: #1028.

## Why

**Persona: embedder.** An embedder builds a server with sweep persistence. The embedder needs a tested store that isolates namespaces by fence and refuses a pool that may not run sweeps, so the later claimer work has a safe base.

The user path this change must make work:

1. **SDK.** The embedder imports the new sweep store package and runs its conformance suite against the memory, SQLite and PostgreSQL adapters.
2. **Server start.** The operator starts the server. The baseline migration of each dialect creates the sweep tables.
3. **CLI.** The operator runs `opendustpan backup` and `opendustpan restore` and checks that the sweep tables follow their backup class.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A write carries a stale fence. | The store returns namespace.ErrFenced and changes nothing. Read the namespace again and retry the write with the live fence. | `opendustpan namespace` |
| A target names a pool that is not an operator pool with the sweep purpose. | The store refuses the write with a pool-purpose error. Name an operator pool with the sweep purpose. | `opendustpan status` |

Evidence for the claims above:

- The ports FindingStore and ScanStore exist today under internal/unmanaged, with no SQL adapter named in the pack. Proof: rg -n -F --glob '!*_test.go' 'FindingStore' pkg internal cmd gives internal/unmanaged/ports.go:249. The same search for ScanStore gives internal/unmanaged/ports.go:326.
- Writes in other domains carry namespace.Fence and map a stale fence to namespace.ErrFenced. Proof: rg for namespace.Fence gives 1280 matches. rg for namespace.ErrFenced gives 489 matches, for example pkg/manuallock/manuallocktest/suite.go:237.
- PostgreSQL tests read their URL from DUSTPAN_TEST_POSTGRES_URL. Proof: internal/servertest/servertest.go:51 holds const EnvURL = DUSTPAN_TEST_POSTGRES_URL. Issue 1028 acceptance criteria name the same variable.

## What Changes

- New package pkg/unmanaged (it is new in this change) with the FindingStore and ScanStore ports and an in-memory reference adapter.
- New conformance suite of the sweep store, written first and run on all three adapters.
- SQLite and PostgreSQL adapters with baseline migration edits and sqlc queries.
- A fence check in the same transaction as each write.
- A pool check in the store: the pool belongs to the namespace of the target, and it is an operator pool with the sweep purpose.
- A decision on the backup class of the sweep tables, stated in the pull request.

Out of scope:

- The sweep claimer and its wiring.
- The sweep routes and the management routes.
- The match and findings logic.
- Other parts of the split of add-job-session-paths-runner.

Depends on:

- `add-job-session-paths-runner-2` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- No route ships in this part, so a user cannot reach the store alone. The docs page says that the last part completes the path.
- A stale fence error names the one command that reads the live namespace.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 163: The resource index and unmanaged resources found from state** (not owner-tagged).

- Decisions that apply: 13 (current).
- Changed by: ADR 166 changes decisions 5 and 11; ADR 210 changes; ADR 216 changes decision 9. Chain to the end: ADR 166, ADR 210, ADR 216, ADR 286, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 166, 210, 216.
- Quote: "`unmanaged.Index` holds the index: `ReplaceUnit`, `SetDeleted`, `DropUnit`, `Holders` and `Contested`. Each write carries the fence of the namespace of the unit."
- Effect: The same fence rule applies to the sweep store: each write carries the fence.
- Conflict: none.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 5 (changed), 18 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "`unmanaged.ScanStore` holds targets and sweeps: `CreateTarget`, `UpdateTarget`, `DeleteTarget`, `Targets`, `StartScan`, `AdvanceScan`, `Scan` and `Scans`. Each write carries the fence."
- Quote: "A sweep pool runs only sweeps. A runner pool gets a **purpose**: `jobs` or `sweeps`. A sweep target must name a pool of the purpose `sweeps`, and a job must name a pool of the purpose `jobs`."
- Effect: ScanStore checks the fence and the pool. A target must name a pool of the purpose sweeps. By the owner decision, that pool is an operator pool.
- Conflict: none.

**ADR 165: Unmanaged-resource findings and the actions on them** (not owner-tagged).

- Decisions that apply: 13 (current), 14 (current).
- Changed by: ADR 166 changes decision 15; ADR 210 changes. Chain to the end: ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210.
- Quote: "`unmanaged.FindingStore` holds findings and rules: `Record`, `ResolveUnseen`, `Get`, `List`, `Act`, `ExpireIgnores`, `ExpirePending`, `PutRule`, `DeleteRule` and `Rules`. Each write carries the fence."
- Quote: "Bounds. A page of findings is at most 500. One call to `Record` takes at most 1,000 observations."
- Effect: FindingStore has the listed operations, a fence on each write and the stated bounds.
- Conflict: none.

**ADR 166: Context and graph: gaps found before the implementation** (not owner-tagged).

- Decisions that apply: 5 (current), 10 (current).
- Changed by: ADR 210 changes; ADR 286 changes. Chain to the end: ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 210, 286.
- Quote: "The backup class of the resource index: rebuildable."
- Quote: "The stores key by the namespace ID. Every row of the context store, of the resource index, of the projection heads and of the dependency edges holds the stable ID of the namespace"
- Effect: Rows key by namespace ID. Sweep targets and findings hold user decisions, so they are not projection tables.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: The store only keeps data. It adds no cloud adapter to the server. The pool check keeps sweeps off user runners.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): proposal.md and spec.md refuse a target pool without sweep purpose but never state the owner rule (P14): a namespace pool with the sweep purpose must also be refused, and the spec has no scenario for it. Resolution: answered: The owner rule (queue P14) is in the spec, the design and the scenarios: a namespace pool with the sweep purpose is refused.
- R1.2 (missing scenario): The spec has no requirement for the bounds of decision 14 of ADR 165 (page at most 500, Record batch at most 1000) or for the jobs-pool refusal; the design layer table requires them but the spec does not state them. Resolution: answered: The spec now has the bounds requirements and the jobs-pool scenario.
- R1.3 (missing scenario): No scenario covers a write whose namespace has no row, is soft-deleted, or whose fence is equal to the stored value; the fence check has only a stale case. Resolution: answered: The fence requirement has equal, missing and soft-deleted scenarios.
- R1.4 (failure mode): Concurrency of the in-transaction fence check is not specified: two writers with the same fence, or a fence bump between check and write under READ COMMITTED on PostgreSQL, are not covered by any test in tasks.md. Resolution: answered: The atomic fence requirement and a concurrency task cover the race.
- R1.5 (adr conflict): ADR 164 decision 18 requires that the store check the pool belongs to the namespace and that a sweep pool has the sweep purpose; the spec omits the namespace-of-pool check entirely. Resolution: answered: The requirement Pool of the namespace covers ADR 164 decision 18.
- R1.6 (open question): The ErrScanBusy scenario gives no rule for a sweep that has the status expired or canceled; the spec does not say whether these end the sweep for the busy check. Resolution: answered: Considered and the spec say that succeeded, failed, canceled and expired end the busy check, from ADR 164 decision 15.
- R1.7 (wrong claim): The proposal says the open questions section lists none, yet it records failing code checks (pkg/unmanaged missing, pool command missing); the evidence line for the pool error command names a command that does not exist. Resolution: answered: The missing package is named as new in what_changes. The error command is now a real command, and the open questions are none.
- R1.8 (scope): Task 2.2 wires the store into the server and requires a make e2e sweep-store step, while the proposal says the store has no route and no user path; the e2e check cannot be written for a store without a surface. Resolution: answered: The e2e step is moved to the last part. This part has a wiring test.
- R1.9 (missing scenario): Backup round trip: the design says the tables are covered by the round-trip test or a control-table note, but tasks 1.5 accepts either; no decision is made on whether the sweep tables belong to the backed-up or projection class. Resolution: answered: Considered and design decide that the sweep tables are normal backed-up tables, not projection tables.
- R1.10 (failure mode): No scenario covers a restart or outage during a write that has already committed the fence check but not the row change; the atomicity of the SQL transaction is assumed, not tested. Resolution: answered: The rollback scenario and the forced-error test cover a failure after the fence check.

## Open questions

none

Questions considered and answered:

- Must each store write carry a fence? Yes. ADR 164 decision 18 and ADR 165 decision 13 say each write carries the fence, and the store checks it in the same transaction.
- Which pool may a sweep target name? Owner decision, 13 Nov 2026 (queue P14): only an operator pool with the sweep purpose. A namespace pool with the sweep purpose is refused, and so is a pool with the jobs purpose. ADR 164 decisions 5 and 18 add that the pool must belong to the namespace of the target, which the store checks.
- Which backup class do the sweep tables have? Targets, sweeps, findings and ignore rules hold decisions and history that a state check cannot compute again. They are not projection tables under ADR 166 decision 5. They are normal tables that the backup list holds, keyed by namespace ID (ADR 166 decision 10).
- Which sweeps end the busy check? ADR 164 decision 15 gives the statuses. A sweep that ended is succeeded, failed, canceled or expired. Only a sweep not in these statuses makes a second start fail with ErrScanBusy.
- Does this part have a user route or an e2e step? No. The routes ship in the last part. This part proves the store with the conformance suite and the backup round trip.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
