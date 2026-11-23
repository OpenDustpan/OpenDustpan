Issue: #3289.

Decision: ADR 370 and ADR 394. A fix change for the review (rev2, security) of the pull request of `housekeeping-principals-install`, which found that a restored row can take the ID of a housekeeping principal. Part of #3929.

## Why

**Persona: operator.** An operator restores a backup, and the operator may not have made the archive (a team member made it, or someone changed it in the backup bucket). The operator needs the restored installation to hold only the housekeeping principals that the code defines. Today a row of the `principals` table with the ID `prn-housekeeping-history-retention`, the kind `person`, the source `oidc` and the subject of an outside account loads without a check. Each namespace that the operator creates after the restore then gives that row the housekeeping roles, so one crafted archive gives a person hidden access to every future namespace. The access review shows these grants as grants of the server to a housekeeping principal.

The user path this change must make work:

1. **API.** The operator restores a backup with the existing route `POST /api/dustpan/v1/restores`. A backup that holds a principal with an ID that starts with `prn-housekeeping-`, or with the source `builtin`, that is not exactly one of the built-in housekeeping principals, is refused with the existing error `backup data is corrupt`. The installation stays as it was.
2. **API.** The operator creates a namespace with the existing route `POST /api/server/namespaces`. When a housekeeping principal row is missing or is not the built-in row of its purpose, the server creates no namespace and answers with the existing error for a failed namespace create. The audit log has one entry with the outcome `denied` that names the principal.
3. **API.** A person with `server:manage-principals` tries `DELETE /api/server/principals/{id}` on a housekeeping principal. The server refuses it with the existing error `reserved subject`. The same person tries `POST /api/server/principals` with a subject that starts with `housekeeping:`. The server refuses it with the same error.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The backup has a changed housekeeping row. | `backup data is corrupt`, with the ID of the row. | Restore the archive again from a copy that was not changed. |
| A housekeeping principal row is missing or changed. | The namespace create fails and names the principal ID. | `GET /api/server/principals?kind=service&source=builtin` |
| A delete or a create names a housekeeping principal. | `reserved subject`. | `GET /api/server/principals?kind=service&source=builtin` |

Evidence for the claims above:

- The restore stage checks staged grants of namespace principals and the IDs of grants, and no step checks a row by the ID prefix or by the source `builtin`. Proof: the facts "The stage runs these checks" and "The stage check of namespace principal grants" of "What main already does".
- The principal delete refuses the namespace service subject and no other reserved subject. Proof: the fact "The delete of a principal".

## What Changes

- NEW: a stage check `checkStagedHousekeepingPrincipals` in both dialects. Each staged row of `principals` with an ID that starts with `prn-housekeeping-`, or with the source `builtin`, must equal one member of `principal.HousekeepingPurposes()` (NEW in `housekeeping-principals-install`): the ID, the subject, the kind `service`, the source `builtin`, an empty namespace and `break_glass` of 0. Any other row makes the backup corrupt (`backup.ErrCorrupt`). The check lists the ID of the row and no other value of it. The stage also refuses a purpose that has no row, because a staged installation without the row would give no grant on the next namespace.
- NEW: the write of the grants of a created namespace reads the principal row of each housekeeping grant in the same transaction and fails closed when the row is missing or is not the built-in principal of that purpose (the same equality as the stage check). The service writes one audit entry with the outcome `denied` and the ID of the principal in the audit chain of the server, and it creates no namespace.
- NEW: the subject prefix `housekeeping:` is reserved. `CreatePrincipal` and each sign-in path that makes a principal refuse a subject that starts with it, with the existing `principal.ErrReservedSubject`. The seeded rows are not made through these paths.
- NEW: `DeletePrincipal` refuses a principal with the source `builtin`, with `principal.ErrReservedSubject`. A person can still revoke each grant of a housekeeping principal (ADR 370 decision 3).
- Test side only: a conformance case "a grant with no ID is ErrInvalid" for `CreateNamespaceWithGrants` (`CreateNamespaceWithGrants` is NEW in `housekeeping-principals-install`, and it adds the store guard `grants[i].ID == ""`, and no test fails when a mutation removes it).

Out of scope:

- The role table and the reach of the grant of `housekeeping-agent-reaper` on `@server`: the owner decides them (ADR 394), and a separate change builds the answer.
- The check that a grant of a created namespace has an object inside that namespace, and the narrow actions for the token policy and the issuer settings.
- The refusal of a credential, a session or a token for a housekeeping principal, and the refusal of an out-of-purpose grant: those are part 3, `housekeeping-principals`.

Depends on:

- housekeeping-principals-install

Builds on (not a change):

- ADR 370 and ADR 394 on main

## Usability

Where the path still costs the person time or surprises them:

- A restore that fails the check names the row but not the reason it changed. The operator checks the archive and its source. The docs page `docs/guides/backup-restore-upgrade.md` gets one row in its table of restore errors.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference. The housekeeping principals, their stored grants and `CreateNamespaceWithGrants` come with `housekeeping-principals-install`, so no fact below names them; task 0.1 reads them on main after that change lands.

- The stage runs these checks, and none of them reads a row by the ID prefix `prn-housekeeping-` or by the source `builtin`. (`internal/store/sqlite/backup.go:954 (func checkStagedRows)`)
- The stage check of namespace principal grants refuses a grant that leaves the namespace of its principal, and returns `backup.ErrCorrupt`. (`internal/store/sqlite/backup_grants.go:100 (func checkStagedNamespacePrincipalGrants)`, `internal/store/postgres/backup_grants.go:97`)
- The delete of a principal refuses the namespace service subject and no other reserved subject. (`pkg/principal/service.go:267 (Service.DeletePrincipal)`, `pkg/principal/principal.go:369 (func refuseNamespaceServiceSubject)`)
- The sentinel for a reserved subject exists. (`pkg/principal/principal.go:50 (ErrReservedSubject)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 370: Housekeeping principals replace the namespace service principal** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current) and 3 (current, amended by ADR 394).
- Changed by: ADR 394 changes decision 3 (owner, 2026-11-22). Chain to the end: ADR 394.
- Current rule: this ADR, with ADR 394.
- Quote: "Each housekeeping purpose has its own built-in service principal (owner, 2026-11-14)."
- Quote: "A revoke of the grant of one housekeeping principal stops only its own work, and the server writes the denial to the audit log."
- Effect: This change makes decision 2 true against a crafted backup and a changed row: the set of housekeeping principals is the set that the code defines. Decision 3 stays: a person can revoke each grant, and the delete refusal covers the principal, not the grants.
- Conflict: none.

**ADR 394: Housekeeping grants at the namespace creation** (owner-tagged (all decisions)).

- Decisions that apply: the grants of a created namespace.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Effect: The point-of-use read protects the grants that this ADR makes at each namespace creation. It adds no grant and no role.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged).

- Decisions that apply: each layer stops the attack alone; fail closed; a test for each step.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Effect: The stage check and the point-of-use read are two layers. Each has its own hostile test, and each carries a defense-in-depth comment.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decisions 1, 2, 4 and 5).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Effect: No schema change here, so no baseline edit.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Is the stage check enough? No. A row can also change in the database after the restore, so the namespace creation reads the row at the point of use (ADR 250: each layer stops the attack alone).
- Does the stage refuse a missing purpose row? Yes. A backup made by this version always holds all rows, because the baseline seeds them, and a missing row gives no grant on the next namespace and so a silent loss of a purpose.
- Which error does the namespace creation return? The existing error of a failed create, with the denial in the audit log. A new error code is not needed because the operator's next step is the list command, which is in the error table above.
- Does the reserved prefix block a person who wants a subject `housekeeping:...`? Yes, as the prefix `namespace-service:` is reserved already, and no user needs the name.
- Why not remove the check of an existing delete route? The delete of a built-in principal has no use: the set is fixed by the code and only the grants are data (ADR 370).
- What about N1 of the review (a grant object outside the namespace) and N3 (narrow token and issuer actions)? Not here. N1 needs a conformance case of its own and N3 is an owner decision about the role table.

Drafted by the PM from the rev2 review of the pull request of `housekeeping-principals-install` (rev2, 23 November 2026).
