---
version: 7
---

Issue: #1069.

## Why

**Persona: namespace administrator.** During an incident, a namespace administrator must lock many stacks and workspaces at once, each with the same reason, and later remove those locks. Today the person sends one request for each unit.

The user path this change must make work:

1. **API.** Send one call with a reason and 1 to 100 unit IDs to the bulk-lock route. Read one result for each unit.
2. **API.** Send one call with 1 to 100 unit IDs to the bulk-unlock route. Read one result for each unit.
3. **API.** Read the lock of a unit that the call locked, with the single lock routes, and see the same holder and reason.
4. **Docs page.** Read docs/guides/manual-locks.md for the two calls and their results.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A bulk call names 0 units or more than 100. | 400 with error code lock.bulk-size, and nothing changes. | Send 1 to 100 unit IDs. |
| A unit of the call already has a manual lock. | The result of that unit has lock.already-locked and names the holder. The other units keep their results. | `GET /api/inventory/unit-lock?unit={unit reference}` |
| A bulk-unlock names a unit whose lock another person holds. | The result of that unit has lock.not-holder. The lock stays. | the force-unlock route of the unit, which needs `unit:manage` (see the guide) |
| The caller cannot lock a unit that the caller can read. | The result of that unit has the error of a lock call on that unit alone: 403. | Ask a person with `unit:lock` on the unit. |
| A unit ID does not exist, is in another namespace, or the caller cannot read the unit. | The same result for all three: 404 with lock.target-not-found. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

## What Changes

Two NEW routes on the tool-neutral API, for units only:

- NEW route `POST /api/dustpan/v1/namespaces/{namespace}/unit-locks/bulk-lock`: the body holds one `reason` and `units`, an array of 1 to 100 objects with the stable `id` of a unit.
- NEW route `POST /api/dustpan/v1/namespaces/{namespace}/unit-locks/bulk-unlock`: the body holds `units`, as above. It removes the lock that the caller holds, as the single unlock does.

For each unit, the server does what the single lock or unlock route does for that unit alone: it resolves the unit, checks the actions, changes the lock and writes the audit entry in the same transaction. The call is not atomic. A unit that fails does not stop or undo the others. The answer is 200 with one result for each unit, in the order of the request.

The routes take the `Idempotency-Key` header. A retry answers the stored result of each unit that the first call changed, and does again each unit that failed with 5xx or 429.

A new error code (NEW), `lock.bulk-size`, covers a call with no unit or with more than 100. A new body limit of 20480 bytes covers the 500-character reason in its longest JSON form (6000 bytes) and 100 unit IDs of up to 100 characters each.

The change updates docs/guides/manual-locks.md, docs/reference/inventory-api.md, the API description and the generated clients, and adds one e2e step.

Out of scope:

- Bulk calls on collections and on namespaces. A namespace is one object, and a collection lock already covers its units. A later change can add a kind.
- A bulk force-unlock. ADR 309 decision 2 names "lock, unlock" only.
- The batch record. ADR 309 decision 1 says "Batch is a record of its own" and no change on main or in `openspec/changes` specifies that record. The bulk-lock and bulk-unlock answers hold no batch ID in this change. A later change can add the field and the record without a break.
- Installation scope lock, import holds, freeze schedules, the cover of state restore, delete, stack import and stack rename, and the console display. These stay as in the earlier slices.

Depends on: none. Every part of the manual-lock work that earlier drafts of this change listed is on main (see "What main already does").

## Usability

Where the path still costs the person time or surprises them:

- One call replaces up to 100 calls, and each result names the unit by its ID, so a script maps results to units without a second read.
- A unit that does not exist and a unit that the caller cannot read give the same result, so a bulk call tells no more than the single calls do.
- A retry with the same `Idempotency-Key` is safe: a unit that the first call locked is not locked a second time.
- The guide shows the two calls with their output, and the e2e step runs them.

## What main already does

Each fact names its file, line and function on main (checked on main at 8c76edc53). The change specifies only the difference.

- Namespace locks, collection locks and unit locks exist. `manuallock.KindNamespace`, `manuallock.NamespaceTarget` and the covering check are in `pkg/manuallock/manuallock.go:75` (const KindNamespace) and `pkg/manuallock/manuallock.go:113` (func NamespaceTarget). The SQL adapter reads the covering locks in `pkg/store/postgres/manual_locks.go:175` (func CoveringLocks). The shipped work is the merge of the pull request that replaced the closed first attempt.
- The namespace lock covers every unit, and a test guards it. (`pkg/manuallock/manuallock_test.go:522` (func TestManuallockNamespaceCover))
- The job dispatch check refuses a covered unit with `ErrLocked`, in the transaction that claims the phase, and the conformance suite tests it for a unit lock and for a namespace lock. (`pkg/job/store.go:244` to `:268`; `pkg/job/jobtest/dispatch.go:405` (func testLocked) and `:441` (func testNamespaceLocked))
- A tool lock of a phase that started before the lock passes, and equal or missing times refuse. (`pkg/manuallock/service.go:260` (func CoveringFor); tests at `pkg/manuallock/manuallock_test.go:929`, `:1005`, `:1180` and `:1233`)
- The single lock routes of a unit, a collection and a namespace sit in one route table. `lockRoutes` is at `internal/api/inventory/locks.go:53`, and its rows start at `:97`. Their operation names are in `internal/api/inventory/operations.go:94` to `:122`, and their paths in `internal/api/refusal/lockroutes.go:17` to `:21`.
- `putLock`, `deleteLock` and `forceUnlock` call `Service.Lock`, `Service.Unlock` and `Service.ForceUnlock` with an audit entry. Each service call checks `unit:lock` (or `unit:manage`) on the object at the point of use, and the store writes the lock and the audit entry in one transaction. (`internal/api/inventory/locks.go:194` (func putLock) to `:276` (end of func forceUnlock); `pkg/manuallock/service.go:93` (func Lock), `:136` (func Unlock), `:338` (func authorize))
- The error classes of a lock call are in `classifyLock`. (`internal/api/inventory/locks.go:353` (func classifyLock); codes in `internal/errcode/catalog_lock.go:5` to `:20`)
- A bulk call exists for another area and sets the precedent: at most 100 units, one result for each unit, not atomic, one audit entry for each unit, and a replay record for each unit with the `Idempotency-Key` header. (`internal/api/settings/serve.go:407` (func bulkPut); `internal/api/settings/keyed.go:204` (func beginBulk) and `:303` (func bulkItemKeyed); `internal/api/settings/settings.go:59` (const MaxBulkUnits))
- The guide documents single locks, namespace locks and the job rules. (`docs/guides/manual-locks.md`, section "The object of a lock")
- An e2e test drives a namespace lock through the API and both CLIs. (`e2e/manual_lock_test.go:419` (func TestManualLockOnNamespaceStopsTheChangeRoutes))

What main lacks, and this change builds:

- The two bulk routes, their operations, their handler and their tests. `rg -n "bulk-lock|bulk-unlock|unit-locks" internal pkg e2e docs openspec/specs` finds nothing.
- The error code `lock.bulk-size`.
- The guide section, the reference rows and the e2e step of the bulk calls.
- The page `docs/guides/manual-locks.md` is still on the pending list of the docs command check. Moving the whole page to the checked list is not part of this change; only the commands of the bulk section get a step.

## ADR check

Each quote is copied from the file in `docs/adr/`. The "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ...)", or when the ADR records decisions of the project owner.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (owner-tagged: decision 10; decision 5 is not tagged).

- Decisions that apply: 5 (extended by ADR 309), 10 (changed by ADR 241 and ADR 244; not touched here).
- Quote, decision 5: "Bulk calls: at most 100 IDs, each item on its own. A bulk call takes from 1 to 100 IDs of one object kind. A call with more answers 400 and does nothing."
- Quote, decision 5: "The call is not atomic: an item that fails does not stop or undo the others. The answer is 200 with one result for each ID, in the order of the request: the ID, `ok` or the error of ADR 182 for that item."
- Quote, decision 5: "So a bulk call tells no more about which objects exist than single calls do."
- Quote, decision 5: "A retry of a bulk call is safe with the `Idempotency-Key` header (ADR 210 decision 6)."
- Quote, decision 10: "Manual locks (owner decision 2026-10-18). A person can put a manual lock with a reason on one unit, one collection or a whole namespace."
- Effect: the bulk routes follow decision 5 exactly: per-item results, not atomic, 1 to 100 IDs of one kind (unit). The lock objects of decision 10 do not change.
- Conflict: none.

**ADR 309: Bulk actions, batch records and the delete guard** (owner-tagged: it records decisions of the project owner of 18 October 2026).

- Decisions that apply: 1 and 2.
- Changed by: none.
- Quote, decision 2: "First bulk actions: cancel, lock, unlock, ignore findings, plus bulk run start (plan/apply". "each goes through its own gates". "No bulk approve".
- Quote, decision 1: "Batch is a record of its own".
- Effect: bulk lock and bulk unlock are named first actions. "Its own gates" is read as: each item passes the actions of the single call (`unit:lock` on the unit), not a bulk-wide check. Manual locks are fixed controls, not gates (ADR 222 decision 4). The batch record of decision 1 is parked (see Out of scope): this change adds an audit entry for each unit, which a batch record can later group.
- Conflict: none. The shape of the batch record is not decided in any ADR, and this change does not decide it.

**ADR 210: The tool-neutral API prefix, paths, actions, versions and style** (not owner-tagged except decision 6).

- Decisions that apply: 5 and 6.
- Quote, decision 5: "Custom methods use a sub-path segment. An operation that is not a create, a read, an update or a delete is a `POST` on a sub-path of its resource". The ServeMux "cannot match the `{id}:verb` form of Google AIP-136". The earlier draft of this change named `locks:bulk-lock`; that form is not valid, so the routes use sub-path segments.
- Quote, decision 6: "Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18)."
- Effect: routes `.../unit-locks/bulk-lock` and `.../unit-locks/bulk-unlock`, both keyed.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4.
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches, quota reservations and quota counts, the source filter, safe delete (ADR 153 decision 5)"
- Effect: manual locks stay fixed controls. The bulk routes add no gate.
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026: stable IDs** (owner-tagged).

- Quote, decision 1: "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only."
- Effect: a bulk item names a unit by its stable ID, not by its reference.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- All or nothing, or one result for each unit? ADR 213 decision 5: one result for each ID, not atomic. The code precedent is `internal/api/settings/serve.go:407` (func bulkPut). Google AIP-234 prefers atomic batches, and ADR 213 decision 5 says the decision follows the AWS bulk calls because an authorization check on each item must not stop the others.
- Which objects can a bulk call name? Units. ADR 213 decision 5: "IDs of one object kind", and the first bulk actions of ADR 309 decision 2 do not name another kind.
- Does bulk-unlock remove another person's lock? No. It does what the single unlock does: the holder removes the lock. Another holder gives lock.not-holder for that unit.
- What happens to a repeated ID in one call? Each item runs in order and on its own. The second lock of a unit gives lock.already-locked, and the second unlock gives lock.not-locked.
- Does the call create a batch record? No. Parked, see Out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs, and rewritten after an audit of main on 2026-11-19. Every quote, path and ADR citation was checked by code.
