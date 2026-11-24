## Context

ADR 370 gives each housekeeping purpose its own built-in service principal with stored grants. This part makes the principals, their stored grants and the `source` filter that lists them. It is store and list work: no sweep, no authenticator and no authorizer changes here. Part 2 (`housekeeping-principals-sweeps`) makes the sweeps run as their principals. Part 3 (`housekeeping-principals`) refuses a credential and an out-of-purpose grant, and covers the sweeps of the identity packages.

## Decisions

1. **One closed list of purposes.** NEW `principal.HousekeepingPurpose` is a type with unexported fields. Its values come only from NEW `principal.HousekeepingPurposes()`. Each value has a stable subject (`housekeeping:<purpose>`) and a role name. The actions of each role live in `pkg/access/roles.go` and nowhere else, because `pkg/access` imports `pkg/principal`, so the `principal` package cannot hold them. The table "Roles of the housekeeping principals" gives the role name and the exact actions of each purpose. A purpose that is not in the list cannot exist, so no code can act as a housekeeping principal of a made-up purpose.
2. **The baseline holds the principals. The follow-up change writes the grants on `@server`.** The baseline of each dialect holds one principal of kind `service` and source `builtin` for each purpose. For each purpose that acts on the installation, the stored grant of its role on `@server` is written by the install step of the follow-up change `housekeeping-install-grants-audit` (in `Open` of each store, with the server as the creator, the install time and one audit entry for each grant). This part does not write these grants and the baseline does not seed them. These are the six sweep purposes with a `server:*` action, `token-sweep`, and `agent-reaper` (for the reveal sweep, with the installation role `housekeeping-agent-reveal-sweep`, which holds no namespace action: decision 10). Code defines the role. The grant row decides who holds it.
3. **The store filters by source.** The store applies the `source` filter in the list query and in the count query. The service does not filter after the read, because that breaks the page totals and the cursors.
4. **The seed is part of the empty installation.** The principals of the baseline and the grants that the install step of `housekeeping-install-grants-audit` writes exist in every new installation, so the shared suites expect them: each total is the seeded count plus the rows of the test, and the count comes from `principal.HousekeepingPurposes()`. The suites do not filter the seeded rows out and the store does not hide them, because a hidden principal is a rule that a user cannot see (No magic).
5. **A restore replaces the seeded rows with the rows of the backup.** The portable restore stages the backup in a new baseline database. As for the installation row, the token policy and the session policy, the stage deletes the seeded `principals` rows (and any `grants` row, which the baseline no longer seeds) before it loads the table files, because the backup holds its own rows of both tables, with their grants, creators, times and revokes. Keeping the seeded rows would collide with the imported rows (same subject, same grant ID), and keeping them in place of the imported rows would lose a revoke that an operator made (ADR 370 decision 3: "A person can list, audit and revoke them"). The stage code edits `internal/store/sqlite/backup_stage_portable.go` and `internal/store/postgres/backup_stage_portable.go`.

6. **Each namespace creation writes the grants on the namespace (owner, 2026-11-22).** The five roles `housekeeping-agent-run-reaper`, `housekeeping-history-retention`, `housekeeping-issuer-rotation`, `housekeeping-job-reconcile` and `housekeeping-saved-view-cleanup` hold actions that act inside a namespace. A grant on `@server` reaches a namespace only when `access.installationGrantsReachNamespaces` is `true`, and OpenDustpan Cloud locks it `false`. That setting does not exist on main: the change `installation-grants-reach-namespaces-setting` adds it, and this change does not depend on it. So `access.Service.CreateNamespace` (`pkg/access/service.go`) builds one `Grant` for each of the five roles on `identity.NamespaceObject(ns.Name)`, with the creator set to the server subject and the time set to the creation time. The role list is the closed list of `principal.HousekeepingPurposes()` filtered by "acts inside a namespace" (a field of the purpose, not a second list in the access package). The namespace, the grant of its creator, the five grants and their audit entries commit in one transaction: the port method `CreateNamespaceWithGrant` of `access.GrantStore` takes a list of grants (NEW name `CreateNamespaceWithGrants`, with its conformance cases first). Each housekeeping grant has an audit entry in the chain of the new namespace, with the person who created the namespace as the actor and the reason "housekeeping grants of namespace creation". The path of the bootstrap administrator, which has no stored principal and no creator grant, writes the five grants too, because they belong to the housekeeping principals and not to the creator. A grant that the operator revokes stays revoked: no code writes it again, because the write happens only at the creation of a namespace. The revoke rule is in `docs/reference/code-rules.md` and `docs/guides/roles-and-grants.md` (task 2.9). The server writes no grant for a namespace that already exists (no installation exists, ADR 322), and the restore of a backup brings the grants of the backup (decision 5). The path `namespace.Service.Create` without an access service stays for test builds only, and the server wires the access service always.
7. **Six narrow actions, one for each sweep (owner, 2026-11-22).** NEW `server:sweep-artifacts`, `server:sweep-blobs`, `server:sweep-checkpoints`, `server:prune-name-locks`, `server:expire-temporary-administrators` and `server:sweep-agent-reveals`, in `pkg/identity/identity.go`, in the list of all actions and in the `families` map of `pkg/access/access.go` as `FamilyOperator`. Each role holds exactly one. The prefix `server:` stays as on main. A later rename to `installation:` follows the rename of the area, in its own change.
8. **One action for the reconcile of jobs (owner, 2026-11-22).** NEW `job:reconcile`, in `pkg/identity/identity.go`, in the list of all actions and in the `families` map as `FamilyData`, with a unit as its object, as the cancel action has. The role `housekeeping-job-reconcile` holds this action only. It holds no start action, so the principal cannot start a plan, an apply or a destroy, and it holds no `job:cancel`. The job service checks `job:reconcile` for each change that the reconcile makes to a job that a person started: part 2 builds the check and the new return value of `reconcileAction`. A grant of `job:reconcile` to a person is possible like any other grant, and it gives no start action.
9. **Two unclassified actions get a family.** `unit:redact` and `namespace:manage-agent-runs` are in `pkg/identity/identity.go` and not in the `families` map on main. This part adds both as `FamilyData`, like the other actions on a namespace, a collection or a unit. It changes no built-in role on its own.
10. **No role granted on `@server` holds a namespace action (owner, 2026-11-23, "Split the role").** The purpose `agent-reaper` holds two roles: the installation role `housekeeping-agent-reveal-sweep` (only `server:sweep-agent-reveals`, granted on `@server` at the install) and the namespace role `housekeeping-agent-run-reaper` (only `namespace:manage-agent-runs`, granted on each namespace at its creation, one of the five grants of decision 6). One role with a server action and a namespace action, granted on `@server`, made a revoke of the namespace grant have no effect: the action stayed allowed through the grant on `@server`. With the split, the revoke of the namespace grant denies `namespace:manage-agent-runs` on that namespace. The rule is a property of the closed list: no role in `HousekeepingPurposes()` that is granted on `@server` holds an action of a namespace area, and the test of task 1.4 checks it. The port, the principal and the audit reason do not change. A purpose is one principal that holds one or two roles.

## Structure

- `HousekeepingPurpose` with unexported fields: a housekeeping principal of an unknown purpose cannot exist.
- The authorizer has no branch for a housekeeping principal: its grants are rows, like any grant. This makes the bug class "a principal that acts on a rule in code" impossible for housekeeping.
- No custom analyzer and no new tool (ADR 364).

## Layers

This part adds no guard. The table of hostile cases for each guard is in part 3. The grants of a created namespace have their own conformance case on both dialects (task 1.5): five grants of the five roles of decision 6, a creator and a time, in the same transaction as the namespace, so a failed grant leaves no namespace. The restore has its own case on both dialects (task 1.3): the staged rows are the rows of the backup. The store layer has its conformance case: the install principals exist on both dialects, with their grants, a creator and a time, and the `source` filter keeps the total and the cursor of its rows only.

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

- No NEW table. The baseline of each dialect holds one row in `principals` (kind `service`, source `builtin`) for each purpose. The stored grant of each purpose with a grant on `@server` is written by the install step of `housekeeping-install-grants-audit`, with the server as the creator, the install time and one audit entry; it is not a baseline row.
- The namespace creation writes five rows in `grants` and five audit entries in the chain of the new namespace, in the transaction of the namespace row. No schema change.

Port (`pkg/access/access.go:561`):

- `GrantStore.CreateNamespaceWithGrant` becomes `CreateNamespaceWithGrants(ctx, ns, grants []Grant, e audit.Entry)`. Each grant has an ID and the store appends its audit entry in the chain of the namespace. An empty list stores the namespace alone, as a grant with no ID does today. The conformance cases of `accesstest` come first.

Actions (`pkg/identity/identity.go`): NEW `server:sweep-artifacts`, `server:sweep-blobs`, `server:sweep-checkpoints`, `server:prune-name-locks`, `server:expire-temporary-administrators`, `server:sweep-agent-reveals` and `job:reconcile`. Each is in the list of all actions and in `families`.

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

This table is the contract of the roles. Part 2 and part 3 use it as it is. The NEW constants, descriptions and action lists go in `pkg/access/roles.go` (the constant block, `builtInDescriptions` and `builtInActions`), in the style of `RoleTokenManager`: a lower-case name with hyphens, and a comment that says what the role may do. Each role name is `housekeeping-<purpose>`, and the NEW constant is `RoleHousekeeping<Purpose>`, with one exception: the purpose `agent-reaper` has two roles, `housekeeping-agent-reveal-sweep` (`RoleHousekeepingAgentRevealSweep`) and `housekeeping-agent-run-reaper` (`RoleHousekeepingAgentRunReaper`) (for example `RoleHousekeepingArtifactSweep = "housekeeping-artifact-sweep"`). A role holds only the actions in its row, and no built-in role gets any of them (the family lists stay as on main, except the NEW actions and the two classifications of decisions 7 to 9). The owner decided the open rows on 22 November 2026 (decisions 6 to 8).

The "Action values" column names the actions as `identity.Action` values. The caller of each purpose has no authorizer call on main, so the action of a row comes from the audit entry that the purpose writes now, or, when that name is not an `identity.Action`, from the NEW action of decision 7 or 8.

| Purpose | Subject | Role name | Action values | Grant objects, and who writes the grant | Evidence on main |
| --- | --- | --- | --- | --- | --- |
| `agent-reaper` (installation) | `housekeeping:agent-reaper` | `housekeeping-agent-reveal-sweep` | NEW `server:sweep-agent-reveals` (the sweep of expired reveals). Only this action. | `@server` (install) | (read only) `internal/agent/reaper.go` (line 53) (`Reap`); (read only) `pkg/identity/identity.go` (line 529) |
| `agent-reaper` (namespace) | `housekeeping:agent-reaper` | `housekeeping-agent-run-reaper` | `namespace:manage-agent-runs` (ends the orphan runs). Only this action. | Each namespace (namespace creation) | (read only) `internal/agent/reaper.go` (line 53) (`Reap`) |
| `artifact-sweep` | `housekeeping:artifact-sweep` | `housekeeping-artifact-sweep` | NEW `server:sweep-artifacts`. The audit action `job:artifact-sweep` is not an `identity.Action`. | `@server` (install) | (read only) `pkg/job/artifactsweep.go` (line 33) and `:654` |
| `blob-sweep` | `housekeeping:blob-sweep` | `housekeeping-blob-sweep` | NEW `server:sweep-blobs`. | `@server` (install) | (read only) `pkg/workspace/service.go` (line 2527) (`deleteExpiredBlobs`) |
| `checkpoint-sweep` | `housekeeping:checkpoint-sweep` | `housekeeping-checkpoint-sweep` | NEW `server:sweep-checkpoints`. | `@server` (install) | (read only) `pkg/stack/superseded_sweep.go` (line 72) (`SweepSuperseded`) |
| `history-retention` | `housekeeping:history-retention` | `housekeeping-history-retention` | `unit:redact` (a redaction of the cause `retention`). | Each namespace (namespace creation) | (read only) `pkg/history/redaction.go` (line 22) (`RetentionReason`) and `:45` (`CauseRetention`); (read only) `pkg/identity/identity.go` (line 598); (read only) `internal/api/jobs/routes.go` (line 193) |
| `issuer-rotation` | `housekeeping:issuer-rotation` | `housekeeping-issuer-rotation` | `namespace:manage-issuer` | Each namespace (namespace creation) | (read only) `internal/issuersvc/settings.go` (line 197) (`serverEntry`) and `:98` (the check of a person) |
| `job-reconcile` | `housekeeping:job-reconcile` | `housekeeping-job-reconcile` | NEW `job:reconcile`. No start action and no cancel action. | Each namespace (namespace creation) | (read only) `pkg/job/reconcile.go` (line 279), `:340` (`ActionJobCancel`) and `:451` (`reconcileAction`, which returns the first start requirement) |
| `name-lock-prune` | `housekeeping:name-lock-prune` | `housekeeping-name-lock-prune` | NEW `server:prune-name-locks`. The audit action `sign-in.name-locks-pruned` is not an `identity.Action`. | `@server` (install) | (read only) `internal/localusersvc/locks.go` (line 359) and `:510` |
| `saved-view-cleanup` | `housekeeping:saved-view-cleanup` | `housekeeping-saved-view-cleanup` | `view:delete` | Each namespace (namespace creation) | (read only) `pkg/savedview/service.go` (line 266) (`systemEntry`) |
| `temp-admin-expiry` | `housekeeping:temp-admin-expiry` | `housekeeping-temp-admin-expiry` | NEW `server:expire-temporary-administrators`. The audit action `server:manage-temporary-administrators` is not an `identity.Action`. | `@server` (install) | (read only) `pkg/tempadmin/tempadmin.go` (line 189); (read only) `pkg/tempadmin/service.go` (line 365) |
| `token-sweep` | `housekeeping:token-sweep` | `housekeeping-token-sweep` | `server:manage-token-policy` | `@server` (install) | (read only) `pkg/accesstoken/policy.go` (line 204) (`SweepEntry`); (read only) `pkg/access/access.go` (line 167) |

Notes:

1. **Object.** A grant on `@server` (identity.ServerObject) is written by the install. A grant on a namespace is written by the creation of that namespace (decision 6): five roles, so each new namespace gets five grants. The five roles never rely on `access.installationGrantsReachNamespaces`. This change tests them in the current mode only (the authorizer always adds `@server` grants to a namespace); the run with the setting `false` is a task of the change `installation-grants-reach-namespaces-setting`. No role has both kinds of grant: the purpose `agent-reaper` holds two roles (decision 10), so that no role granted on `@server` holds a namespace action.
2. **Least privilege.** No role holds a read action that its purpose does not use, and no role holds a delegation action, a token action or `server:manage-principals`. The test of task 1.4 checks this for each role.
3. **Classification.** `unit:redact` and `namespace:manage-agent-runs` get `FamilyData` (decision 9). The six `server:*` actions get `FamilyOperator` (decision 7). `job:reconcile` gets `FamilyData` (decision 8).
4. **The two `agent-reaper` rows.** `Reap` ends orphan runs and sweeps expired sealed reveals. The first needs a grant on a namespace (role `housekeeping-agent-run-reaper`), and the second a grant on `@server` (role `housekeeping-agent-reveal-sweep`). Part 2 makes `Reap` check each action on its own object.
5. **Names.** The new names follow the `server:` prefix of the operator and administration actions on main. ADR 210 decision 7 renames the area to `installation:`; the rename lands in its own change, and the new names follow it then.

## Risks

- Each namespace has five more grants. The review of a large installation grows by five rows for each namespace. The operator can list them, and the docs page of part 3 says why they exist.
- A principal and a grant exist with no sweep that uses them until part 2 and part 3 land. The glossary term and the list make them visible, and nothing acts on them.
- Part 2 and part 3 depend on the `HousekeepingPurpose` type. A change of the closed list after this part lands is a change to this part's contract.
