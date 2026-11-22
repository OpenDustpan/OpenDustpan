Issue: #3289.

Decision: ADR 370. Part 1 of 3 of the housekeeping principals. Part 2 is `housekeeping-principals-sweeps`. Part 3 is `housekeeping-principals`, which keeps the name of the whole work, so each change that waits for "housekeeping-principals" waits for all three parts. This part touches no file of the access token, identity and sign-in packages.

## Why

**Persona: operator.** An operator reads the access review of an installation. Today no principal exists for the housekeeping of the server (the sweeps, the retention, the expiry of temporary administrators), and no stored grant says what it may do. The operator needs each housekeeping purpose to exist as a named principal with stored grants that the operator can list and read. This part makes the principals and their grants. Part 2 and part 3 make each sweep run as its principal.

The user path this part must make work:

1. **API.** The operator lists the principals of the installation: the existing route `GET /api/server/principals?kind=service&source=builtin` (NEW `source` filter and NEW `builtin` source). The answer lists one housekeeping principal for each purpose, for example `housekeeping:artifact-sweep`, with its stable ID.
2. **API.** The operator lists the grants of one housekeeping principal: the existing route `GET /api/server/principals/{principal}/grants`. Each grant has a role, an object, a creator (the install) and a time.
3. **Docs page.** The `source` row of the principal fields in `docs/guides/principals-and-groups.md` lists the `builtin` value.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The `source` filter has a value that is not valid. | 400, as for the `kind` filter. | `GET /api/server/principals?kind=service&source=builtin` |

Evidence for the claims above:

- The list route has the `kind` filter and no `source` filter. Proof: the first three facts of "What main already does".

## What Changes

- NEW: one built-in service principal for each housekeeping purpose, made at the install, with the source `builtin`.
- NEW: the install step writes the grants of each housekeeping principal as stored grants, with the install as the creator and the install time. A role for each purpose holds only the actions that the purpose needs.
- NEW: the `source` filter of `GET /api/server/principals` and the `builtin` source. The other routes exist on main.
- NEW: a portable restore clears the seeded principals and grants of the new baseline before it loads the backup, so the restored installation has the rows of the backup.
- Test side only: the shared suites of principals, namespace principals and access expect the seeded rows in their totals, page orders and review counts.
- Update `GLOSSARY.md`: add "Housekeeping principal".

Out of scope:

- A filter or a hidden rule that removes the seeded rows from a list: the seed is part of the empty installation.
- The sweeps that act as their principals (part 2 and part 3).
- A refusal of a credential or of a grant for a housekeeping principal (part 3).
- The namespace service principal: the first trigger change removes it with the run-as principal that replaces it.
- A route to make a new housekeeping principal. The set is fixed by the code, and only their grants are data.

Depends on:

- none

Builds on (not a change):

- ADR 370 on main (pull request #3058)

## Usability

Where the path still costs the person time or surprises them:

- Until part 3 lands, nothing refuses a person who asks for a grant of any role to a housekeeping principal. The principals exist and are listed, and no sweep uses them yet. The docs page for the operator comes with part 3.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The list route reads the `kind` filter and no `source` filter. (`internal/api/server/principals.go:793 (func listPrincipals)`)
- The store port takes the kind and no source. (`pkg/principal/store.go:58 (Store.ListPrincipals)`)
- The service parses the kind. (`pkg/principal/service.go:200 (Service.ListPrincipals)`)
- The source constants stop at `ldap`, `scim`, `oidc` and `oauth` next to `local`. (`pkg/principal/principal.go:132`)
- The source check refuses any other value. (`pkg/principal/principal.go:649 (func validateSource)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 370: Housekeeping principals replace the namespace service principal** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Each housekeeping purpose has its own built-in service principal (owner, 2026-11-14)."
- Quote: "The install step writes the grants of each housekeeping principal as stored grants, with a creator and a time."
- Quote: "A revoke of the grant of one housekeeping principal stops only its own work, and the server writes the denial to the audit log."
- Effect: This part builds the principals of decision 2 and the stored grants of decision 3 at the install, in the baseline (decision 4). Decision 3 revoke and denial are built by part 2 and part 3. Decision 1 is built by the first trigger change.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decisions 1, 2, 4 and 5).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place."
- Effect: The install grants and the `builtin` source are baseline edits. No migration code.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which purposes get a principal? The eleven purposes of the design document ("Purposes on main"). Task 0.1 checked the list on main on 17 November 2026.
- What happens to the seeded rows on a restore? The stage deletes them before it loads the backup, as it does for the installation row and the two policy rows; the backup holds its own rows. The existing step in `internal/store/sqlite/backup_stage_portable.go` and `internal/store/postgres/backup_stage_portable.go` is the precedent. Design decision 5.
- Does a housekeeping principal belong to a namespace? No. It is a principal of the installation. Its grants name the objects that its purpose touches.
- Who is the creator of the install grants? The install, recorded as the creator with the install time, as ADR 338 decision 1 did for the namespace grant.
- What about the existing namespaces? No installation exists, so no migration (ADR 322).

Drafted by the PM from ADR 370, as part 1 of 3 of the change that was named `housekeeping-principals` alone.
