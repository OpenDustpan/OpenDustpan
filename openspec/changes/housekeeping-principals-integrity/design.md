## Context

`housekeeping-principals-install` seeds one built-in service principal for each housekeeping purpose and writes the grants of the five namespace roles at each namespace creation. The seeded rows are data, so a restore or a database edit can change them. The creation writes grants to the fixed ID `prn-housekeeping-<purpose>` and never reads the row. This is security code (ADR 250): two layers, each with its own hostile test.

## Decisions

1. **One equality.** NEW `principal.IsBuiltinHousekeeping(row)` in `pkg/principal/housekeeping.go` reports whether a principal row equals the built-in row of one purpose: the ID, the subject, the kind `service`, the source `builtin`, an empty namespace and `break_glass` of 0. The stage check and the point-of-use read both call the same rule, in the same file. The stage check reads columns, so each dialect builds a `principal.Principal` from the staged columns and calls the function.
2. **The stage check refuses by prefix and by source.** In each dialect `checkStagedHousekeepingPrincipals` runs in `checkStagedRows` next to `checkStagedGrantIDs`. A staged row with an ID that starts with `prn-housekeeping-`, or with the source `builtin`, that is not one built-in row makes the backup corrupt. A built-in purpose with no staged row makes it corrupt too. The error text names the ID of the row and no other value of it.
3. **The point of use reads in the transaction.** `CreateNamespaceWithGrants` (NEW in `housekeeping-principals-install`) reads the row of each housekeeping subject of its grants by ID inside the transaction that writes the namespace, and calls the same rule. A missing or changed row returns `access.ErrInvalid` wrapped with the ID, and the transaction rolls back, so no namespace row stays. The service writes one audit entry with the outcome `denied` and the ID, in the audit chain of the server, outside the rolled-back transaction.
4. **Fail closed.** An unknown value, a read error and a missing row all refuse. No fallback writes the grant without the read.
5. **The prefix `housekeeping:` is reserved for the seed.** A sibling of `refuseNamespaceServiceSubject` refuses a subject that starts with `housekeeping:` in `CreatePrincipal` and in each sign-in path that makes a principal. The baseline writes the seeded rows directly, so it does not pass through these paths.
6. **A built-in principal is not deleted.** `DeletePrincipal` refuses a principal with the source `builtin` with `principal.ErrReservedSubject`, before the store call. The revoke of a grant stays allowed.

## Contract

- NEW `func IsBuiltinHousekeeping(p Principal) bool` in `pkg/principal/housekeeping.go`. It returns true only when `p` equals the built-in principal of one purpose of `HousekeepingPurposes()` (NEW in `housekeeping-principals-install`).
- NEW `func checkStagedHousekeepingPrincipals(ctx context.Context, db stagedQuerier, staging string) error` in each dialect. It returns `backup.ErrCorrupt` wrapped with the ID of the row.
- `CreateNamespaceWithGrants` returns `access.ErrInvalid` wrapped with the principal ID when a row is missing or changed. No new column, table, endpoint or CLI output.
- NEW denied audit entry: outcome `denied` (`audit.OutcomeDenied`), the ID of the principal as the object, in the audit chain of the server.
- The answers of `POST /api/server/namespaces`, `POST /api/server/principals`, `DELETE /api/server/principals/{id}` and `POST /api/dustpan/v1/restores` keep their existing error shapes.

## Hostile cases

One table for each layer, and for each guard a mutation that removes the guard and fails a test.

| Layer | Hostile input | Expected |
| --- | --- | --- |
| Stage | `prn-housekeeping-history-retention` with kind `person`, source `oidc` | `backup.ErrCorrupt` |
| Stage | a row with source `builtin` and an unknown ID | `backup.ErrCorrupt` |
| Stage | a row with the right ID and a changed subject | `backup.ErrCorrupt` |
| Stage | a row with the right ID and `break_glass` 1 | `backup.ErrCorrupt` |
| Stage | a purpose with no staged row | `backup.ErrCorrupt` |
| Store | the same row in the database, then a namespace create | `access.ErrInvalid`, no namespace, no grant |
| Store | a purpose row deleted from the database, then a namespace create | `access.ErrInvalid`, no namespace |
| Service | the store refuses | one `denied` audit entry with the ID |
| Handler | the service refuses | the existing failed-create answer |
| Principal service | `CreatePrincipal` with subject `housekeeping:x` | `principal.ErrReservedSubject` |
| Principal service | `DeletePrincipal` of a `builtin` principal | `principal.ErrReservedSubject` |

Each redundant check carries a comment that says it is a defense-in-depth layer and names the layers that it does not rely on.

## Risks

- A future purpose changes the list. The equality reads `HousekeepingPurposes()` (NEW in `housekeeping-principals-install`), so the stage and the point of use change with it, and an older backup then fails the check for the purpose it lacks. No tag shipped (ADR 322), so no upgrade path.
