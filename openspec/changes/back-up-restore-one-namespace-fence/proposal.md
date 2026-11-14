---
version: 1
---

Issue: #959.

## Problem

A namespace administrator backs up one namespace and restores it after a bad change. Today only whole-installation restore exists, so a restore would touch other namespaces. This change restores only the named namespace, keeps other namespaces unchanged, and keeps an undo path.

## Evidence

- Main has namespace-scoped route tables and scope helpers the change builds on. Proof: pkg/gate/scope.go:97 (func NamespaceScope) and internal/api/server/routes.go:183 (func Handler.restoreRoutes)
- Main has portable namespace row helpers and durable operation plumbing. Proof: pkg/store/storetest/portable_namespaces.go:16 (func PortableNamespaceRows) and pkg/store/postgres/durable.go:44 (func Store.DurableOperations)

## Fix boundary

- Namespace restore route scoped to one namespace as a durable operation.
- Writer fence, restore checks, grant and home-principal handling, concurrent maintenance isolation.
- Pre-restore safety backup with key keep and tombstone and redaction replay.

Out of scope:

- Portable export format changes (part 1).
- Single-unit restore (never, per issue #959).
- Whole-installation restore behavior.

## Check

- Add failing server test in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)` and NEW `internal/api/server/namespace_restore_test.go` that backs up A, changes A and B, restores A; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore` and its gate. Check: `TestNamespaceRestoreOnlyTargetNamespace` fails on main in NEW `internal/api/server/namespace_restore_test.go`.
- Add failing tests in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)` and NEW `internal/api/server/namespace_restore_test.go` for wrong-scope backup, missing blob, unopenable key, and concurrent isolation; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore` and its gate. Check: `TestNamespaceRestoreChecks` fails on main in NEW `internal/api/server/namespace_restore_test.go`.
- Add the NEW restore route in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)` and NEW `internal/api/server/namespace_restore.go` with namespace grant, writer fence, tombstone replay, safety backup, and audit entries. Check: `POST /api/dustpan/v1/namespaces/{namespace}/restore` with access-token credential answers 202 in `internal/api/server/namespace_restore.go`.
- Add user docs page in NEW `docs/guides/namespace-backup-restore.md` for namespace backup and restore including restore under another key. Check: NEW `docs/guides/namespace-backup-restore.md` renders and `make docs-lint` passes for changed files.
- Wire the route in `internal/api/server/routes.go:183 (func Handler.restoreRoutes)`, run e2e against the real CLI with an access token through NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore`; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore` and its gate. Check: E2e restores namespace A against the real CLI with an access token while B keeps changes; BLOCKED by NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore`.
- Run full checks with zero findings. Check: `make check` reports zero findings.

Depends on:

- back-up-restore-one-namespace-backup-export
- `back-up-restore-one-namespace-backup-export` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main scopes gate checks to a namespace. (`pkg/gate/scope.go:97 (func NamespaceScope)`)
- Main has installation restore routes behind the server adapter. (`internal/api/server/routes.go:183 (func Handler.restoreRoutes)`)
- Main keeps durable operations in the store. (`pkg/store/postgres/durable.go:44 (func Store.DurableOperations)`)
- Main has portable namespace row test helpers. (`pkg/store/storetest/portable_namespaces.go:16 (func PortableNamespaceRows)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 218: What a namespace is for, and when to make a namespace or a collection (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: ADR 248 changes decisions 1, 2 and 5; ADR 286 changes. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248, 286.
- Quote: "One namespace can be backed up and restored (owner)."
- Effect: Follows the owner decision that one namespace can be backed up and restored, with portable format scope namespace and wrapped namespace key records.
- Conflict: none.

**ADR 30: Backups and restore** (not owner-tagged).

- Decisions that apply: 1 (changed), 8 (changed).
- Changed by: ADR 35 changes decision 9; ADR 38 changes decisions 8 and 10; ADR 38 extends decision 9; ADR 39 changes decision 10; ADR 41 changes decision 7; ADR 42 changes decisions 1, 3, 4, 6 and 8; ADR 151 changes decision 8; ADR 210 changes; ADR 218 extends decision 1; ADR 248 changes decisions 1, 6 and 8; ADR 281 changes decision 2. Chain to the end: ADR 35, ADR 38, ADR 39, ADR 41, ADR 42, ADR 151, ADR 210, ADR 218, ADR 248, ADR 281, ADR 272, ADR 48, ADR 70, ADR 212, ADR 237, ADR 238, ADR 245, ADR 60, ADR 103, ADR 146, ADR 240, ADR 352, ADR 253, ADR 154, ADR 217, ADR 220, ADR 247, ADR 286, ADR 340, ADR 364, ADR 72, ADR 142, ADR 216, ADR 222, ADR 227, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369.
- Current rule: this ADR, with ADR 35, 38, 39, 41, 42, 151, 210, 218, 248, 281.
- Quote: "Scope of milestone 3. The server makes and restores a backup of the whole installation."
- Effect: Follows whole-installation backup and restore order; this part scopes the same steps to one namespace and adds a safety backup before apply.
- Conflict: none.

**ADR 42: A portable backup format** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 248 changes; ADR 253 changes decision 4. Chain to the end: ADR 248, ADR 253, ADR 286.
- Current rule: this ADR, with ADR 248, 253.
- Quote: "The portable layout."
- Effect: Reuses the portable layout and blobs from the first part; this part does not change the export format.
- Conflict: none.

**ADR 38: Restore through the server, in maintenance mode** (not owner-tagged).

- Decisions that apply: 1 (current), 8 (current), 9 (extended).
- Changed by: ADR 42 changes decision 3; ADR 48 extends decisions 4 and 6; ADR 70 changes decision 6; ADR 151 changes decision 3; ADR 210 changes; ADR 212 extends decision 9; ADR 248 changes decision 3. Chain to the end: ADR 42, ADR 48, ADR 70, ADR 151, ADR 210, ADR 212, ADR 248, ADR 253, ADR 72, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 154, ADR 217, ADR 220, ADR 247, ADR 286, ADR 232, ADR 235, ADR 301, ADR 365, ADR 73, ADR 75, ADR 103, ADR 126, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 149, ADR 297.
- Current rule: this ADR, with ADR 42, 48, 70, 151, 210, 212, 248.
- Quote: "Restore is an API operation."
- Effect: Adds the namespace restore as an API durable operation with check before apply and a safety backup, scoped to the namespace.
- Conflict: none.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 22 (current), 24 (current).
- Changed by: ADR 220 changes decision 18. Chain to the end: ADR 220, ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 220.
- Quote: "A backup holds wrapped keys only."
- Effect: Keeps wrapped keys only in the backup and requires each namespace key version that wraps a key in the backup.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does namespace restore conflict with ADR 30 whole-installation scope? No. ADR 218 decision 4 expressly adds namespace backup and restore and ADR 30 decision 1 deferred it; this change implements that extension.
- Does this change alter the portable export format? ADR 42 decision 2 fixes the portable layout and decision 4 fixes the manifest; the roadmap says this part uses the export format from part 1 and does not change it.
- How does the writer fence stop other writers without a per-namespace maintenance mode? Restore bumps the namespace failover version at start of apply and the restore writer carries the new fence; stale writers with the old fence fail the store fence check.
- Where does the restore backup come from? Backup ID from the configured backup target in the request body; no upload path; a backup of namespace A restored into B is refused before any live write.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
