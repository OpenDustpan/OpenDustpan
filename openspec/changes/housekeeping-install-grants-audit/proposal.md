---
version: 1
---

Issue: #3289.

Decision: ADR 370 decision 3 ("A person can list, audit and revoke them") and ADR 338 decision 1. A follow-up of `housekeeping-principals-install` (part 1 of 3), made after the review (rev2, security) of its pull request. That review found that the seven grants on `@server` sit in both baseline files with `created_at_ms = 0`, no creator that is the install, and no audit entry.

## Why

**Persona: operator.** An operator reads the access review of a new installation and asks who gave each housekeeping principal its grant on `@server`, and when. A row with time 0 and no audit entry answers neither question. This breaks "Nothing without a record" and the rule "Each automatic action writes a record of who or what did it and why".

The user path this change must make work:

1. **API.** The operator starts a new installation, then reads the access review with the existing route `GET /api/server/access-review?subject=principal:<id>` for a housekeeping principal that acts on the installation. Each of the seven grants has the install as its creator and the install time (not 0).
2. **API.** The operator reads the audit log with the existing route `GET /api/audit/entries`. For each of the seven grants there is one entry in the chain of `@server`, with the actor `@server` and the reason "housekeeping grants of the install".
3. **API.** The operator restarts the server. No second grant and no second audit entry appear.
4. **API.** The operator restores a backup. Each of the seven grants is the one of the backup (its creator, time and revoke), and the next start writes none again.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The install cannot write the grants (the store refuses the transaction). | The server does not start, and the start log says `housekeeping install failed` with the cause. No grant is half written: the transaction rolls back. | `opendustpan serve` again, or `GET /health/ready` for the state. |

Evidence for the claims above:

- The seven grants are seeded in both baseline files with time 0. Proof: lines 2079-2085 of the SQLite baseline and 1814-1820 of the PostgreSQL baseline in the pull request of `housekeeping-principals-install` (not on main yet).
- The restore copies the installation row from the live database. Proof: `internal/store/sqlite/restore.go:778`.

## What Changes

- NEW: the install code of each store adapter writes the seven grants of the roles that act on the installation (one for each purpose with a grant on `@server`: `artifact-sweep`, `blob-sweep`, `checkpoint-sweep`, `name-lock-prune`, `temp-admin-expiry`, `token-sweep` and the installation role of `agent-reaper`). Each grant has the server as its creator and the install time. Each grant has one hash-chained audit entry in the chain of `@server`. All of it commits in one transaction.
- NEW: the install runs once. A column `housekeeping_installed_at_ms` in the `installation` row (NULL in the baseline) is the record. The step sets it in the same transaction as the grants, with a conditional update (`WHERE housekeeping_installed_at_ms IS NULL`), so two servers that start together on PostgreSQL write the grants once.
- NEW: the restore keeps the value of the live installation row and loads the grants of the backup, so a restored installation holds no double grant and the install step writes nothing after it.
- CHANGED: the seven grant rows leave the baseline of both dialects. The principal rows stay in the baseline (decision 2 of `housekeeping-principals-install`).
- Update `housekeeping-principals-install`: its design, proposal and spec say that this change owns the install time and the audit entry of the seven grants.

Depends on:

- `housekeeping-principals-install`

Out of scope:

- The grants on namespaces: they are written at namespace creation, with their own audit entries (decision 6 of `housekeeping-principals-install`).
- A creator and a time for the principal rows (a principal has no such field).
- The sweeps that use the grants (part 2 and part 3).

## Usability

An operator finds who gave each housekeeping principal its grant on `@server`, and when, from the access review and the audit log alone, with no database read. The check is in tasks 3.1 (usability run-through by a person).

## What main already does

- Main has no housekeeping principal and no grant of a housekeeping role: `rg HousekeepingPurposes pkg` finds nothing on main. This change builds on the code of `housekeeping-principals-install`.
- Both stores open the database and migrate in `Open` (`internal/store/sqlite/sqlite.go:338`, `internal/store/postgres/postgres.go:244`). The PostgreSQL migration holds an advisory lock in one transaction (`internal/store/postgres/postgres.go:1494`).
- The audit append computes the hash only inside a transaction that orders the appends of the scope (`internal/store/sqlite/audit.go:189`).
- The installation row is in the baseline (`INSERT INTO installation (id, state, kind) VALUES (1, 'normal', NULL)`), and the restore copies it from the live database (`internal/store/sqlite/restore.go:778`).

## ADR check

**ADR 370: Housekeeping principals** (decision 3).

- Quote: "A person can list, audit and revoke them".
- Effect: A grant with time 0, no creator that is the install and no audit entry cannot be audited. This change gives each grant a creator, a time and an entry.
- Conflict: none.

**ADR 338: Namespace creation writes the creator grant** (decision 1).

- Effect: The install is the creator of the install grants, as the namespace creation is for its grant. Same entry shape.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged).

- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place."
- Effect: The new column is a baseline edit, and the removal of the seven rows is a baseline edit. No migration code.
- Conflict: none.

**ADR 250: Defense in depth.** The install is security code (grants). Task 1.3 lists the hostile cases for each layer.

- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the install run? In `Open` of each store adapter, after the migration, so `cmd/opendustpan serve` and `dustpan.OpenStore` get it with no change of `cmd/opendustpan`. A step in `cmd/opendustpan` would let a test or an SDK user open a store that has no grants.
- How does the install know it ran? A column on the installation row, set in the transaction of the grants. A count of rows would write the grants again after an operator removed them, which is a silent rule (No magic).
- Why not keep the rows in the baseline with a real time? A baseline has no install time: it is the same for every installation.
- Does the restore need a clear step for these grants? No. The restore stage is built from the migration only, so it holds none of them, and the backup brings its own.

Drafted by the PM from the rev2 finding 3 of the pull request of `housekeeping-principals-install` and the decision of the lead (option 3).
