## Context

ADR 370 gives each housekeeping purpose its own built-in service principal with stored grants. This part makes the principals, their stored grants and the `source` filter that lists them. It is store and list work: no sweep, no authenticator and no authorizer changes here. Part 2 (`housekeeping-principals-sweeps`) makes the sweeps run as their principals. Part 3 (`housekeeping-principals`) refuses a credential and an out-of-purpose grant, and covers the sweeps of the identity packages.

## Decisions

1. **One closed list of purposes.** NEW `principal.HousekeepingPurpose` is a type with unexported fields. Its values come only from NEW `principal.HousekeepingPurposes()`. Each value has a stable subject (`housekeeping:<purpose>`) and a role name. The actions of each role live in `pkg/access/roles.go` and nowhere else, because `pkg/access` imports `pkg/principal`, so the `principal` package cannot hold them. The table "Roles of the housekeeping principals" gives the role name and the exact actions of each purpose. A purpose that is not in the list cannot exist, so no code can act as a housekeeping principal of a made-up purpose.
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

- NEW type `principal.HousekeepingPurpose`, with unexported fields. A value comes only from the closed list of NEW `principal.HousekeepingPurposes()`. Each value has the subject `housekeeping:<purpose>` and a role name. The actions of the role are in `pkg/access/roles.go` (see "Roles of the housekeeping principals").
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

## Roles of the housekeeping principals

This table is the contract of the roles. Part 2 and part 3 use it as it is. The NEW constants, descriptions and action lists go in `pkg/access/roles.go` (the constant block, `builtInDescriptions` and `builtInActions`), in the style of `RoleTokenManager`: a lower-case name with hyphens, and a comment that says what the role may do. Each role name is `housekeeping-<purpose>`, and the NEW constant is `RoleHousekeeping<Purpose>` (for example `RoleHousekeepingArtifactSweep = "housekeeping-artifact-sweep"`). A role holds only the actions in its row, and no built-in role gets any of them (the family lists stay as on main). No family list changes, except the two classifications in note 3.

The "Action values" column names the actions as `identity.Action` values on main ((read only) `pkg/identity/identity.go`). The caller of each purpose has no authorizer call on main, so the action of a row comes from the audit entry that the purpose writes now, or, when that name is not an `identity.Action`, from the action that a person needs for the same effect. A row that says "OPEN" has no action on main or needs an owner choice. Such a row is not final, and its role is not written until the question is answered (proposal, "Open questions").

| Purpose | Subject | Role name | Action values | Object scope | Evidence on main |
| --- | --- | --- | --- | --- | --- |
| `agent-reaper` | `housekeeping:agent-reaper` | `housekeeping-agent-reaper` | `namespace:manage-agent-runs` (ends the orphan runs). The sweep of expired reveals: OPEN Q2. | Each namespace (OPEN Q1) | (read only) `internal/agent/reaper.go` (line 53) (`Reap`); (read only) `pkg/identity/identity.go` (line 529) |
| `artifact-sweep` | `housekeeping:artifact-sweep` | `housekeeping-artifact-sweep` | OPEN Q2: none on main. The audit action `job:artifact-sweep` is not an `identity.Action`. Proposal: NEW `server:sweep-artifacts`. | `@server` | (read only) `pkg/job/artifactsweep.go` (line 33) and `:654` |
| `blob-sweep` | `housekeeping:blob-sweep` | `housekeeping-blob-sweep` | OPEN Q2: none on main. Proposal: NEW `server:sweep-blobs`. | `@server` | (read only) `pkg/workspace/service.go` (line 2527) (`deleteExpiredBlobs`) |
| `checkpoint-sweep` | `housekeeping:checkpoint-sweep` | `housekeeping-checkpoint-sweep` | OPEN Q2: none on main. Proposal: NEW `server:sweep-checkpoints`. | `@server` | (read only) `pkg/stack/superseded_sweep.go` (line 72) (`SweepSuperseded`) |
| `history-retention` | `housekeeping:history-retention` | `housekeeping-history-retention` | `unit:redact` (a redaction of the cause `retention`). | Each namespace (OPEN Q1) | (read only) `pkg/history/redaction.go` (line 22) (`RetentionReason`) and `:45` (`CauseRetention`); (read only) `pkg/identity/identity.go` (line 598); (read only) `internal/api/jobs/routes.go` (line 193) |
| `issuer-rotation` | `housekeeping:issuer-rotation` | `housekeeping-issuer-rotation` | `namespace:manage-issuer` | Each namespace (OPEN Q1) | (read only) `internal/issuersvc/settings.go` (line 197) (`serverEntry`) and `:98` (the check of a person) |
| `job-reconcile` | `housekeeping:job-reconcile` | `housekeeping-job-reconcile` | OPEN Q3. Derived from code: `job:cancel` and the start action of the job (`unit:run-plan`, `unit:run-apply`, `unit:run-destroy`, `unit:write-back`). | Each namespace (OPEN Q1) | (read only) `pkg/job/reconcile.go` (line 279), `:340` (`ActionJobCancel`) and `:451` (`reconcileAction`, which returns the first start requirement) |
| `name-lock-prune` | `housekeeping:name-lock-prune` | `housekeeping-name-lock-prune` | OPEN Q2: none on main. The audit action `sign-in.name-locks-pruned` is not an `identity.Action`. Proposal: NEW `server:prune-name-locks`. | `@server` | (read only) `internal/localusersvc/locks.go` (line 359) and `:510` |
| `saved-view-cleanup` | `housekeeping:saved-view-cleanup` | `housekeeping-saved-view-cleanup` | `view:delete` | Each namespace (OPEN Q1) | (read only) `pkg/savedview/service.go` (line 266) (`systemEntry`) |
| `temp-admin-expiry` | `housekeeping:temp-admin-expiry` | `housekeeping-temp-admin-expiry` | OPEN Q2: none on main. The audit action `server:manage-temporary-administrators` is not an `identity.Action`. Proposal: NEW `server:expire-temporary-administrators`. | `@server` | (read only) `pkg/tempadmin/tempadmin.go` (line 189); (read only) `pkg/tempadmin/service.go` (line 365) |
| `token-sweep` | `housekeeping:token-sweep` | `housekeeping-token-sweep` | `server:manage-token-policy` | `@server` | (read only) `pkg/accesstoken/policy.go` (line 204) (`SweepEntry`); (read only) `pkg/access/access.go` (line 167) |

Notes:

1. **Object.** A row that says `@server` is a grant on the installation object (`identity.ServerObject`). A row that says "Each namespace" names actions of the data family: a grant on `@server` reaches them only when the setting `access.installationGrantsReachNamespaces` is `true`. Q1 asks how the grant is made.
2. **Least privilege.** No role holds a read action that its purpose does not use, and no role holds a delegation action, a token action or `server:manage-principals`. The test of task 1.4 checks this for each role.
3. **Classification.** `unit:redact` and `namespace:manage-agent-runs` are in the action list of (read only) `pkg/identity/identity.go` (lines 601 and 532) and not in the `families` map of `pkg/access/access.go` (checked by `rg` on main). A role can hold them only after the build classifies them, as `FamilyData`, like the other actions on a namespace, a collection or a unit. This is a NEW line in `pkg/access/access.go` for each action. It changes no built-in role on its own. The classification is not final until Q1 is answered, because Q1 may move them out of the data family.
4. **The `agent-reaper` row** names the end of an orphan run. The sweep of expired sealed reveals in the same `Reap` call has no action on main (Q2).
5. **Proposed names** in Q2 follow the `server:` prefix of the operator and administration actions on main (ADR 210 decision 7 renames the area to `installation:`; the rename lands in its own change, and the new names follow it then).

## Risks

- A principal and a grant exist with no sweep that uses them until part 2 and part 3 land. The glossary term and the list make them visible, and nothing acts on them.
- Part 2 and part 3 depend on the `HousekeepingPurpose` type. A change of the closed list after this part lands is a change to this part's contract.
