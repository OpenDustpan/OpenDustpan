---
version: 2
---

Issue: #1069.

## Why

**Persona: namespace administrator.** A namespace administrator tries to stop all new jobs for a unit, a collection or a whole namespace during an incident, and to see the lock with its reason.

The user path this change must make work:

1. **API.** Lock a unit, a collection or a namespace with a reason through the existing lock routes.
2. **CLI.** Run OpenTofu and the Pulumi CLI against a covered unit and read the locked refusal.
3. **API.** Start jobs on a covered unit and see them wait with reason locked.
4. **API.** Lock or unlock up to 100 IDs with the new bulk routes.
5. **Docs page.** Read docs/guides/manual-locks.md for namespace locks and dispatch behavior.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| OpenTofu lock request on a covered workspace is refused. | 409 with error code lock.locked, the holder and the reason. | `GET /api/dustpan/v1/namespaces/{namespace}/lock` |
| Start of a mutating Pulumi update on a covered stack is refused. | 409 with error code lock.locked, the holder and the reason. | `GET /api/dustpan/v1/namespaces/{namespace}/lock` |
| Bulk call with more than 100 IDs is refused. | 400, and nothing changes. | `GET /api/dustpan/v1/namespaces/{namespace}/lock` |

Evidence for the claims above:

- The first slice put manual locks in the shared core with a conformance suite. Proof: pkg/manuallock
- The guide for manual locks exists and must grow. Proof: docs/guides/manual-locks.md
- Namespace lock and force-unlock routes exist on main. Proof: route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/lock and POST /api/dustpan/v1/namespaces/{}/lock/force-unlock in the pack main list

## What Changes

- Cover each unit of a namespace with the existing namespace lock, including the inventory flag.
- New routes for bulk lock and bulk unlock: POST /api/dustpan/v1/namespaces/{namespace}/locks:bulk-lock and POST /api/dustpan/v1/namespaces/{namespace}/locks:bulk-unlock, with per-item results for 1 to 100 IDs.
- Job dispatch waits on covered units: no new starts and no queued dispatches. Queued jobs wait with reason locked. Running phases end.
- A tool lock with the job token of a phase that started before the lock passes. Equal, missing or racing times refuse.

Out of scope:

- Installation scope lock
- Import holds beyond the preview pass
- Freeze schedules
- Cover of state restore, delete, stack import and stack rename
- Console display, which belongs to the console repository

Depends on:

- add-job-service-routes-start-1

## Usability

Where the path still costs the person time or surprises them:

- A namespace freeze needs one call; bulk lock avoids one call per unit.
- A locked refusal names the holder, the reason and the read lock route.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Lock error codes exist in the catalog. (`internal/errcode/catalog_lock.go:13`)
- The manual lock service has helpers for locked collections. (`pkg/manuallock/service.go:229`)
- The conformance fixture builds locked units. (`pkg/manuallock/manuallocktest/fixture.go:145`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (owner-tagged: decision 10).

- Decisions that apply: 5 (extended), 9 (changed), 10 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "Manual locks (owner decision 2026-10-18). A person can put a manual lock with a reason on one unit, one collection or a whole namespace."
- Quote: "A lock is a flag, not a status. The inventory keeps the lock of a unit as a flag, with its holder, beside the status."
- Effect: Namespace is an object of unit:lock. Bulk calls follow decision 5. The inventory keeps the lock as a flag.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches, quota reservations and quota counts, the source filter, safe delete (ADR 153 decision 5)"
- Effect: Manual locks stay fixed controls, not gates.
- Conflict: none.

**ADR 244: The move check, the guide pages and the e2e steps** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "**A preview under the import hold (supersedes in part ADR 213 decision 10).** While the manual lock of an import covers a unit, the Pulumi Cloud-compatible API accepts the start of an update of the kind `preview` on that"
- Effect: A preview keeps passing under an import hold.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: contract method Covered: no requirement or scenario names it.

Questions considered and answered:

- Which objects can a manual lock cover? ADR 213 decision 10: unit, collection and namespace.
- Is the dispatch check a gate? ADR 222 decision 4: manual locks are fixed controls.
- What shape do bulk calls take? ADR 213 decision 5: 1 to 100 IDs, per-item results, not atomic.
- Which other routes does a lock cover? None added here; they are out of scope, so no open question stays.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
