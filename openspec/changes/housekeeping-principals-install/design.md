## Context

ADR 370 gives each housekeeping purpose its own built-in service principal with stored grants. This part makes the principals, their stored grants and the `source` filter that lists them. It is store and list work: no sweep, no authenticator and no authorizer changes here. Part 2 (`housekeeping-principals-sweeps`) makes the sweeps run as their principals. Part 3 (`housekeeping-principals`) refuses a credential and an out-of-purpose grant, and covers the sweeps of the identity packages.

## Decisions

1. **One closed list of purposes.** NEW `principal.HousekeepingPurpose` is a type with unexported fields. Its values come only from NEW `principal.HousekeepingPurposes()`. Each value has a stable subject (`housekeeping:<purpose>`), a role name and the actions of that role. A purpose that is not in the list cannot exist, so no code can act as a housekeeping principal of a made-up purpose.
2. **The install writes principals and grants.** In the transaction that makes the installation, the store writes one principal of kind `service` and source `builtin` for each purpose, and one stored grant of its role on its object, with the install as the creator and the install time. Code defines the role. The grant row decides who holds it.
3. **The store filters by source.** The store applies the `source` filter in the list query and in the count query. The service does not filter after the read, because that breaks the page totals and the cursors.
4. **The seed is part of the empty installation.** The principals and grants of the baseline exist in every new installation, so the shared suites expect them: each total is the seeded count plus the rows of the test, and the count comes from `principal.HousekeepingPurposes()`. The suites do not filter the seeded rows out and the store does not hide them, because a hidden principal is a rule that a user cannot see (No magic).
5. **A restore replaces the seeded rows with the rows of the backup.** The portable restore stages the backup in a new baseline database. As for the installation row, the token policy and the session policy, the stage deletes the seeded `grants` and `principals` rows before it loads the table files, because the backup holds its own rows of both tables, with their grants, creators, times and revokes. Keeping the seeded rows would collide with the imported rows (same subject, same grant ID), and keeping them in place of the imported rows would lose a revoke that an operator made (ADR 370 decision 3: "A person can list, audit and revoke them"). The stage code edits `internal/store/sqlite/backup_stage_portable.go` and `internal/store/postgres/backup_stage_portable.go`.

## Structure

- `HousekeepingPurpose` with unexported fields: a housekeeping principal of an unknown purpose cannot exist.
- The authorizer has no branch for a housekeeping principal: its grants are rows, like any grant. This makes the bug class "a principal that acts on a rule in code" impossible for housekeeping.
- No custom analyzer and no new tool (ADR 364).

## Layers

This part adds no guard. The table of hostile cases for each guard is in part 3. The restore has its own case on both dialects (task 1.3): the staged rows are the rows of the backup. The store layer has its conformance case: the install principals exist on both dialects, with their grants, a creator and a time, and the `source` filter keeps the total and the cursor of its rows only.

## Contract

Types and functions (`pkg/principal`):

- NEW type `principal.HousekeepingPurpose`, with unexported fields. A value comes only from the closed list of NEW `principal.HousekeepingPurposes()`. Each value has the subject `housekeeping:<purpose>`, a role name and the actions of that role.
- NEW `principal.Housekeeping(purpose)` returns the `identity.Principal` of one purpose.
- NEW source `builtin`, a constant next to `SourceLocal` (`pkg/principal/principal.go:132`). `validateSource` (`pkg/principal/principal.go:649`) accepts it. An unknown source returns `principal.ErrInvalid`.

Port (`pkg/principal/store.go:58`):

- `ListPrincipals` of `principal.Store` takes the source filter next to the kind. An empty source keeps every source. The total and the cursor count only the rows of the filter. It returns `principal.ErrInvalid` for an unknown source.

Endpoint (existing route, NEW query parameter):

- `GET /api/server/principals?kind=service&source=builtin` keeps the principals of that source. As for `kind`, a source that is not valid answers 400.

Tables:

- No NEW table. The install writes one row in `principals` (kind `service`, source `builtin`) for each purpose, and one stored grant of its role, with the install as the creator and the install time, in the baseline of each dialect.

## Purposes on main (task 0.1, checked on 17 November 2026)

The caller of each purpose on main is in the design document of its part. The rule: a caller is a housekeeping purpose when the server starts the run itself (a schedule, a sweep, a reaper or a timer) and no person asked for it. A caller that writes `@server` only as the sender of a notification, or as an effect of the request of a person, is not a purpose. This follows the practice of a service account for each background job with the least role that the job needs.

| Purpose | Part that makes it run as its principal |
| --- | --- |
| `artifact-sweep` | part 2 |
| `job-reconcile` | part 2 |
| `saved-view-cleanup` | part 2 |
| `history-retention` | part 2 |
| `name-lock-prune` | part 2 |
| `agent-reaper` | part 2 |
| `checkpoint-sweep` | part 2 |
| `blob-sweep` | part 2 |
| `token-sweep` | part 3 |
| `temp-admin-expiry` | part 3 |
| `issuer-rotation` | part 3 |

This part writes the principal and the grants of all eleven purposes, so the sweeps of part 2 and part 3 find them.

## Risks

- A principal and a grant exist with no sweep that uses them until part 2 and part 3 land. The glossary term and the list make them visible, and nothing acts on them.
- Part 2 and part 3 depend on the `HousekeepingPurpose` type. A change of the closed list after this part lands is a change to this part's contract.
