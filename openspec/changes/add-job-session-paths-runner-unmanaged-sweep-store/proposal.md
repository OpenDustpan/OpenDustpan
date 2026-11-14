---
version: 2
---

Issue: #1028.

## Why

**Persona: embedder.** An embedder builds a server with sweep persistence. The embedder needs a tested store that isolates namespaces by fence and refuses a pool that may not run sweeps, so the later claimer work has a safe base.

The user path this change must make work:

1. **SDK.** The embedder imports the sweep store package and runs its conformance suite against the memory, SQLite and PostgreSQL adapters.
2. **Server start.** The operator starts the server. The baseline migration of each dialect creates the sweep tables.
3. **Docs page.** The embedder reads the page of the sweep store ports and errors, and runs the suite on a new adapter.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A write carries a stale fence. | The store returns namespace.ErrFenced and changes nothing. Read the namespace again and retry with the live fence. | `GET /api/dustpan/v1/namespaces/{namespace}` |
| A target names a pool that is not an operator pool with the sweep purpose. | The store refuses the write with a pool-purpose error. Name an operator pool with the sweep purpose. | `GET /api/dustpan/v1/namespaces/{namespace}` |

Evidence for the claims above:

- The ports FindingStore and ScanStore exist today in internal/unmanaged. Proof: rg -n -F --glob '!*_test.go' 'FindingStore' pkg internal cmd gives internal/unmanaged/ports.go:249. The same search for ScanStore gives internal/unmanaged/ports.go:326.
- Writes in other domains carry namespace.Fence and map a stale fence to namespace.ErrFenced. Proof: rg for namespace.Fence gives 1280 matches. rg for namespace.ErrFenced gives 489 matches, for example internal/cost/ports.go:119.
- PostgreSQL tests read their URL from DUSTPAN_TEST_POSTGRES_URL. Proof: internal/servertest/servertest.go:51 holds const EnvURL = DUSTPAN_TEST_POSTGRES_URL.
- The package pkg/unmanaged does not exist yet. Proof: The path check of the pack lists pkg/unmanaged with exists false.

## What Changes

- Create the package pkg/unmanaged with the sweep store ports. The ports of internal/unmanaged are the base. The move is a rename of the package path with no change of behavior.
- An in-memory reference adapter and a conformance suite of the sweep store, written first and run on all three adapters.
- SQLite and PostgreSQL adapters with baseline migration edits and sqlc queries.
- A fence check in the same transaction as each write.
- A pool check in the store: the pool is an operator pool with the sweep purpose, stored by its stable ID. A namespace pool is refused.
- The sweep tables are normal tables of the table list, so a backup includes them.

Out of scope:

- The sweep claimer and its wiring (last part of the split).
- The sweep routes and the management routes (last part of the split).
- The match and findings logic.
- The durable operation that a sweep start creates (the claimer part).
- Other parts of the split of add-job-session-paths-runner.

Depends on:

- `add-job-session-paths-runner-2` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- No route ships in this part, so a user cannot reach the store alone. The docs page says that the last part completes the path.
- A stale fence error names the one command that reads the live namespace.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The FindingStore port exists in the internal package. (`internal/unmanaged/ports.go:249`)
- The ScanStore port exists in the internal package. (`internal/unmanaged/ports.go:326`)
- namespace.ErrFenced is the sentinel of a stale fence. (`pkg/namespace/namespace.go:38`)
- namespace.Fence is the type that a write carries. (`pkg/namespace/namespace.go:178`)
- The PostgreSQL test URL variable is defined. (`internal/servertest/servertest.go:51`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 163: The resource index and unmanaged resources found from state** (not owner-tagged).

- Decisions that apply: 13 (current).
- Changed by: ADR 166 changes decisions 5 and 11; ADR 210 changes; ADR 216 changes decision 9. Chain to the end: ADR 166, ADR 210, ADR 216, ADR 286, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 166, 210, 216.
- Quote: "Each write carries the fence of the namespace of the unit."
- Effect: The same fence rule applies to the sweep store.
- Conflict: none.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 5 (changed), 18 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "`unmanaged.ScanStore` holds targets and sweeps: `CreateTarget`, `UpdateTarget`, `DeleteTarget`, `Targets`, `StartScan`, `AdvanceScan`, `Scan` and `Scans`. Each write carries the fence."
- Quote: "A sweep target must name a pool of the purpose `sweeps`, and a job must name a pool of the purpose `jobs`."
- Effect: ScanStore checks the fence and the pool. By the owner decision, the pool is an operator pool.
- Conflict: none.

**ADR 165: Unmanaged-resource findings and the actions on them** (not owner-tagged).

- Decisions that apply: 13 (current), 14 (current).
- Changed by: ADR 166 changes decision 15; ADR 210 changes. Chain to the end: ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210.
- Quote: "Bounds. A page of findings is at most 500. One call to `Record` takes at most 1,000 observations."
- Effect: FindingStore keeps the listed operations, a fence on each write and the bounds.
- Conflict: none.

**ADR 166: Context and graph: gaps found before the implementation** (not owner-tagged).

- Decisions that apply: 5 (current), 10 (current).
- Changed by: ADR 210 changes; ADR 286 changes. Chain to the end: ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 210, 286.
- Quote: "The backup class of the resource index: rebuildable."
- Effect: Sweep tables hold decisions and history, so they are not projection tables. Rows key by namespace ID.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: The store keeps data only and adds no cloud adapter to the server.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: evidence path `pkg/unmanaged` does not exist in the checkout; a code check fails: evidence path `pkg/unmanaged` does not exist in the checkout; a code check fails: path `pkg/unmanaged` (.what_changes[0]) does not exist in the checkout; use an existing path, or name it as new ("add the new file pkg/unmanaged").

Questions considered and answered:

- Must each store write carry a fence? Yes. ADR 164 decision 18 and ADR 165 decision 13.
- Which pool may a sweep target name? Owner decision, 13 Nov 2026 (queue P14): only an operator pool with the sweep purpose. An operator pool belongs to no namespace. A target names the stable ID of an operator pool. A namespace pool with the sweep purpose is refused.
- Which backup class do the sweep tables have? Normal tables, not projection tables (ADR 166 decision 5), because a state check cannot compute them again.
- Which sweeps end the busy check? ADR 164 decision 15: succeeded, failed, canceled and expired end it. NextScan holds the transitions.
- Who starts the durable operation of a sweep? ADR 164 decision 3: the service that stores the sweep. The claimer part adds it.
- What happens to a pool that is deleted or changed after a target is saved? StartScan re-reads the pool by ID and refuses with the pool-purpose error.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
