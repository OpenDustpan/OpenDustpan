# M4-42a: the access-review export names where a group membership comes from

- Class: SIZED
- Routing: Sonnet
- Branch: `m4-42a-review-membership-source`
- Conflict group: `access-review`
- Persona path: P4 team access, step `access_review_lists_group_member` (#2610)
- Depends on: none. M4-31a makes `claim` memberships in real use; this change
  reads the source columns that exist today. The link to the mapping row
  (ADR 332: "a membership from a rule ... records the rule") is M4-42b, after
  M4-31a.

## Facts checked at main 9e96ae30c8c3

- `pkg/access/review.go`: `ReviewRow.Via *Membership`; `access.Membership`
  has `AddedAt`, `RemovedAt`, `PrincipalID`, `PrincipalSubject`. No source.
- `pkg/principal/principal.go`: the stored membership has `Source`
  (`local`, `scim`, `claim`), `SourceID`, `Pinned`, `SuspendedAt`.
- `pkg/store/sqlite/review.go`, `pkg/store/postgres/review.go`: fill `Via`
  from `group_members`.
- `pkg/access/accesstest/review.go`, `review_more.go`, `caller_grants.go`:
  the suites of `ReviewStore`.
- `internal/api/server/review.go:140`: `reviewViaResponse` with
  `memberUntilMs`, `group`, `member`, `memberSubject`, `memberFromMs`.
- `docs/guides/access-review.md` exists.
- ADR 52 (the export lists each grant with holder, role, object, creator and
  time). CLAUDE.md "No magic": a user can ask why a principal has access.

## Files it may touch

- `pkg/access/review.go` (`Membership`: add `Source string`, `SourceID string`, `Suspended bool`)
- `pkg/access/accesstest/review.go`, `pkg/access/accesstest/review_more.go`, `pkg/access/accesstest/caller_grants.go`
- `pkg/store/sqlite/review.go`, `pkg/store/postgres/review.go`, their `queries.sql` blocks (edit in place), `internal/db` by `make generate`
- `internal/api/server/review.go` and its tests
- `openapi.json`, `clients/typescript` (by the generators only)
- `docs/guides/access-review.md`, `docs/reference/` page of the export if one lists the fields

## Files it must not touch

- `pkg/principal/*`, `internal/signinsvc/*` (M4-31a owns them)
- The schema baselines: no new column is needed

## Change

1. Read `source`, `source_id` and the suspended state of the membership with
   the row of `Via`, in the same snapshot as the page.
2. Answer them in `via` as `memberSource`, `memberSourceId` and
   `memberSuspended`. A `local` membership has an empty `memberSourceId`.
3. A source value that the store reads and that is not one of the three known
   values fails the page with an error (fail closed). Do not map it to `local`.
   Comment: defense in depth, the write path validates the source too.
4. Guide: one short section "Where a membership comes from", with the three
   values and what an auditor does with each.

## Acceptance

- Conformance: a row through a `local`, a `scim` and a `claim` membership each
  returns its source and source ID, on SQLite and PostgreSQL. A suspended
  membership shows `memberSuspended: true` in a snapshot.
- Hostile: a stored row with source `ldap-forged` (written by the test
  directly into the table) fails the read with an error and returns no page.
- Handler test: the JSON field names and the empty source ID of `local`.
- `make clients-check` clean after `make clients-generate`.
- Guide updated in the same PR. `make check` zero findings.
