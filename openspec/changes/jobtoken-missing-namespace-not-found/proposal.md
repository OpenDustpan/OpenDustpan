---
version: 1
---

Issue: #3154.

Short form: bug. Owner lane: D5 (through the lead).

## Problem

**Persona: contributor.** The job token store has one port and three
implementations: the memory double of the suite and the two SQL stores of
the open pull request #2916. For an issue into a namespace that has no row,
the SQL stores return `namespace.ErrNotFound` from their fence check, and the
memory double returns `namespace.ErrFenced`. The port doc does not say which
is right, and no conformance case covers it, so the implementations
disagree and no test catches it.

## Evidence

- The port doc of `jobtoken.Store.Issue`, `pkg/jobtoken/store.go:16-41`,
  lists `namespace.ErrFenced` "when the fence is stale or names another
  namespace than the unit" and gives the order of the rules (:37-41). It
  does not list `namespace.ErrNotFound`.
- The memory double, `pkg/jobtoken/jobtokentest/mem_test.go:221` (func
  memWorld.namespaceOf), returns false when the fence "is stale, names
  another namespace, or names no namespace", and `issueLocked` (:176-178)
  maps every false to `namespace.ErrFenced`.
- The SQL stores of #2916 check the fence with the shared fence read of each
  dialect, which returns `namespace.ErrNotFound` when the namespace has no
  row (for example `checkFenceForUpdate` in `pkg/store/postgres/audit.go:454`).
- The suite cases on main (`pkg/jobtoken/jobtokentest/issue_cases.go`) cover
  a stale fence (:553) and a fence of another namespace (:478), not a
  namespace with no row.

## Fix boundary

- The port doc lists `namespace.ErrNotFound` when the namespace of the fence
  has no row, and puts it before `namespace.ErrFenced` in the order of the
  rules.
- The memory double returns `namespace.ErrNotFound` for a namespace with no
  row, and keeps `namespace.ErrFenced` for a stale fence and a fence of
  another namespace.
- A NEW conformance case runs on the memory double now, and on each SQL
  store when #2916 lands.
- Out of scope: the SQL adapters themselves (they are in #2916 and already
  return `namespace.ErrNotFound`).

## Check

`go test ./pkg/jobtoken/jobtokentest/ -count=1` passes with the new case,
and `make check` reports zero findings (Confirmed by: CI).

## ADR check

No ADR is touched. The change applies the rule in `CLAUDE.md`: "Keep ports
small, and document them with the errors they return." and "Every port has a
conformance suite". Conflict: none.

## Open questions

none. Considered: make the SQL stores return `namespace.ErrFenced` (refused:
`namespace.ErrNotFound` is what the shared fence read of every SQL store
returns for a missing namespace, and a fence that names no namespace is not
a stale failover version).
