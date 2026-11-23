---
version: 1
---

Issue: #3152.

Short form: bug (SECURITY). Owner lane: D5 (through the lead).

## Problem

**Persona: namespace administrator.** A job of namespace `acme` must act
only on units of `acme`. Today the job store accepts a new job whose unit
reference names `acme` but carries the stable unit ID of a unit in
namespace `beta`. The job then counts, locks and stores against the unit of
`beta`. This breaks the rule that a write inside a namespace carries a
fence that the store checks in the same transaction, and the rule that a
stored reference uses the stable ID of the entity.

## Evidence

- `internal/store/jobstore/store.go:460` (func checkNewJob), which the
  SQLite and the PostgreSQL job stores share (`jobstore.New` in
  `internal/store/sqlite/jobs.go` and `internal/store/postgres/jobs.go:41`), compares
  only `j.Spec.Unit.Namespace()` with `fence.Namespace`. It never reads the
  unit row of `j.Spec.Unit.UnitID()`.
- `internal/store/jobstore/store.go:78` (func createIn) uses that unit
  ID in `checkLimits` (:85) and in the stored job, inside the write
  transaction that `Runner.Write` opens after the fence check.
- The memory double of the job suite, `pkg/job/jobtest/mem_test.go:739`
  (func memDB.admit), checks the fence and the namespace of the reference
  only, though it holds the units by ID (`units`, :78).
- The unit ID is unique in the installation: `inventory_unit_id` index
  (`internal/store/sqlite/migrations/0168_baseline.sql:1521`) and the query
  `GetInventoryEntryByUnitID` ("One live unit by its stable ID, which is
  unique in the installation", `internal/store/sqlite/queries.sql:675`).
- The open pull request #2916 adds a job store suite case that builds such
  a job through the harness. Once this change lands, that case must expect
  the refusal.

## Fix boundary

- A structure that makes the bug class impossible: `createIn` reads the
  namespace of the live unit by its ID through a NEW method of
  `jobstore.Tx`, inside the same write transaction, and refuses the job
  with `job.ErrInvalid` (the error that `checkNewJob` returns for a
  reference of another namespace than the fence) when the namespace is
  not the namespace of the fence, or when no live unit has the ID. No job
  row, durable operation or audit entry is written.
- `checkNewJob` keeps its checks. The memory double gets the same check.
- The service and the HTTP layer get their own hostile tests.
- Out of scope: the other writes of the job store that take an existing
  job (they read the job row of the fence namespace already).

## Check

The hostile conformance case passes on SQLite, PostgreSQL and the memory
double, and fails when the new check is removed. `make check` reports zero
findings (Confirmed by: CI).

## ADR check

No ADR is changed. The change applies the rules in `CLAUDE.md`: "Writes
inside a namespace carry a `namespace.Fence` and the store checks it in the
same transaction. Never add a write path that skips the fence." and "Use the
stable ID for each stored reference to an entity that a user can rename, or
remove and make again." **ADR 362** (owner-tagged, decision 1, "(owner,
2026-11-08)") gives the stable-ID rule; this change follows it. Conflict:
none.

## Open questions

none. Considered: (1) return `namespace.ErrFenced` (refused: that error
means a stale failover version, and the client shows its text as sent;
`checkNewJob` already answers `job.ErrInvalid` for a reference of another
namespace); (2) check only in the service (refused: each layer must stop
the attack alone); (3) a database constraint from jobs to inventory
(refused: the jobs table keeps the job of a deleted unit, so a foreign key
would block the delete).
