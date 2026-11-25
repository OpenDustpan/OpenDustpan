---
version: 10
---

Issue: #1028.

## Why

**Persona: embedder.** An embedder builds a server with sweep persistence. The embedder needs a tested store that isolates namespaces by fence and refuses a pool that may not run sweeps (a pool of another namespace or a pool for jobs), so the later claimer work has a safe base.

The user path this change must make work:

1. **SDK.** The embedder imports the sweep store package and runs its conformance suite against the memory, SQLite and PostgreSQL adapters.
2. **Server start.** The operator starts the server. The baseline migration of each dialect creates the sweep tables.
3. **Docs page.** The embedder reads the page of the sweep store ports and errors, and runs the suite on a new adapter.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A write carries a stale fence. | The store returns namespace.ErrFenced and changes nothing. Read the namespace again and retry with the live fence. | `GET /api/dustpan/v1/namespaces/{namespace}` |
| A target names a pool that does not have the sweep purpose, or a pool of another namespace. | The store refuses the write with a pool-purpose error. Name a pool of the namespace of the target that has the sweep purpose. | `GET /api/dustpan/v1/namespaces/{namespace}` |

Evidence for the claims above:

- The ports FindingStore and ScanStore exist today in internal/unmanaged. Proof: rg -n -F --glob '!*_test.go' 'FindingStore' pkg internal cmd gives internal/unmanaged/ports.go:249. The same search for ScanStore gives internal/unmanaged/ports.go:326.
- Writes in other domains carry namespace.Fence and map a stale fence to namespace.ErrFenced. Proof: rg for namespace.Fence gives 1280 matches. rg for namespace.ErrFenced gives 489 matches, for example in the ports of the cost package, line 119.
- PostgreSQL tests read their URL from DUSTPAN_TEST_POSTGRES_URL. Proof: line 51 of the servertest package holds const EnvURL = DUSTPAN_TEST_POSTGRES_URL.

## What Changes

- Add the new package directory for the sweep store ports under pkg, named unmanaged. The ports of internal/unmanaged are the base. The move is a rename of the package path with no change of behavior.
- An in-memory reference adapter and a conformance suite of the sweep store, written first and run on all three adapters.
- SQLite and PostgreSQL adapters with baseline migration edits and sqlc queries.
- A fence check in the same transaction as each write.
- A pool check in the store. The pool has the sweep purpose and belongs to the namespace of the target. The store keeps its stable ID. The store refuses a pool for jobs and a pool of another namespace. No installation pool exists.
- The sweep tables are normal tables of the table list, so a backup includes them.

Out of scope:

- The sweep claimer and its wiring (last part of the split).
- The sweep routes and the management routes (last part of the split).
- The match and findings logic. The 1,000-observation limit of Record is enforced by the memory adapter in this change. The SQL adapters meet it in the change 160-165-report-intake-findings (task 2.1).
- The durable operation that a sweep start creates (the claimer part).
- Other parts of the split of add-job-session-paths-runner.

Depends on:

- None. The part before it in the split is on main.

## Usability

Where the path still costs the person time or surprises them:

- No route ships in this part, so a user cannot reach the store alone. The docs page says that the last part completes the path.
- A stale fence error names the one command that reads the live namespace.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The FindingStore port exists in the internal package. (`internal/unmanaged/ports.go:249`)
- The ScanStore port exists in the internal package. (`internal/unmanaged/ports.go:326`)
- namespace.ErrFenced is the sentinel of a stale fence. (the namespace package, line 38)
- namespace.Fence is the type that a write carries. (the namespace package, line 178)
- The PostgreSQL test URL variable is defined. (the servertest package, line 51)
- A runner pool belongs to one namespace, and its purpose is `jobs` or `sweeps` and never changes. (`pkg/runner/runner.go:358 (func Pool.Validate)`, `pkg/runner/runner.go:385 (func CheckUpdate)`)

## Owner decisions that apply

The project owner decided this on 23 Nov 2026. It binds this change. This section quotes it.

- **Owner decision, 23 Nov 2026 (ADR 396, replaces the 13 Nov 2026 answer P14)**: "Sweeps must run on the user's own runners as we most recently re-designed it." A sweep runs on a runner pool of the team that has the sweep purpose. The host identity of the pool has no write access (ADR 164 decisions 1 and 5, ADR 343 decision 3). The server has no installation sweep pool. The 13 Nov 2026 answer and its line "a sweep never runs on user runners" no longer hold.

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
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9; ADR 396 changes decision 5. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247, ADR 396.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "`unmanaged.ScanStore` holds targets and sweeps: `CreateTarget`, `UpdateTarget`, `DeleteTarget`, `Targets`, `StartScan`, `AdvanceScan`, `Scan` and `Scans`. Each write carries the fence."
- Quote: "A sweep target must name a pool of the purpose `sweeps`, and a job must name a pool of the purpose `jobs`."
- Effect: ScanStore checks the fence and the pool. By the owner decision of 23 Nov 2026, the pool is a pool of the team that has the sweep purpose. ADR 396 changes decision 5 in this way.
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
- Changed by: ADR 396 extends decision 3. Chain to the end: ADR 396.
- Current rule: this ADR, with ADR 396.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: The store keeps data only and adds no cloud adapter to the server.
- Conflict: none.

**ADR 396: Sweeps run on the team's own runners, and the CLI does not audit its own usage errors** (owner-tagged (records decisions of the project owner)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A sweep runs on a runner pool of the team that has the sweep purpose. The host identity of the pool has no write access."
- Effect: The store accepts a pool of the namespace of the target that has the sweep purpose. It has no installation pool.
- Conflict: none. This ADR replaces the 13 Nov 2026 answer P14.

## Open questions

none

Questions considered and answered:

- Must each store write carry a fence? Yes. ADR 164 decision 18 and ADR 165 decision 13.
- Which pool may a sweep target name? Owner decision, 23 Nov 2026 (ADR 396, replaces the 13 Nov 2026 answer P14): a pool of the team that has the sweep purpose. The store refuses a pool for jobs and a pool of another namespace.
- Which backup class do the sweep tables have? Normal tables, not projection tables (ADR 166 decision 5), because a state check cannot compute them again.
- Which sweeps end the busy check? ADR 164 decision 15: succeeded, failed, canceled and expired end it.
- Who starts the durable operation of a sweep? ADR 164 decision 3: the service that stores the sweep. The claimer part adds it.
- What happens to a pool that is deleted or changed after a target is saved? StartScan re-reads the pool by ID and refuses with the pool-purpose error.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
