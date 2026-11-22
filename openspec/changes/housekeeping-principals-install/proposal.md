---
version: 2
---

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
- NEW: the install step writes the grants of each housekeeping principal as stored grants, with the install as the creator and the install time. A role for each purpose holds only the actions that the purpose needs. The role name and the exact actions of each purpose are in the table "Roles of the housekeeping principals" of the design document, and the constants, descriptions and action lists are NEW in `pkg/access/roles.go`.
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

**ADR 216: Access scopes and the reach of the installation grant** (owner-tagged: decision 7).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "With `true`, a grant on `@server` reaches each namespace, as ADR 52 decision 5 says. With `false`, a grant on `@server` gives only the actions of the operator scope. It never gives an action inside a namespace."
- Effect: A role of a housekeeping principal that holds data actions (for example `unit:redact`) works through a grant on `@server` only when the setting is `true`. This change does not decide how the grant is made when the setting is `false`. See Q1.
- Conflict: possible, depending on the answer to Q1. Not decided here.

**ADR 52: Namespaces, collections and units** (decision 5, the grant chain unit, collection, namespace, `@server`).

- Quote: "a grant on `@server` reaches each namespace" (as ADR 216 decision 7 cites it).
- Effect: The rows of the role table that say "Each namespace" depend on this chain and on ADR 216 decision 7.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decisions 1, 2, 4 and 5).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place."
- Effect: The install grants and the `builtin` source are baseline edits. No migration code.
- Conflict: none.

## Open questions

The action of four purposes comes from code: `token-sweep` (final), and `history-retention`, `issuer-rotation` and `saved-view-cleanup` (final except for Q1). The other seven rows of the role table in the design document are marked "OPEN". Nothing in them is decided, and an implementing agent must not guess them. This change is not ready for an agent until the owner answers Q1 to Q3.

**Q1. How does a housekeeping principal reach the namespaces?** (touches the owner-tagged ADR 216 decision 7 and ADR 370 decision 3)

Five roles hold actions of the data family on namespaces: `agent-reaper`, `history-retention`, `issuer-rotation`, `job-reconcile` and `saved-view-cleanup`. A grant on `@server` gives these actions only when `access.installationGrantsReachNamespaces` is `true`. OpenDustpan Cloud locks the value `false`, so a grant on `@server` would stop these purposes there.

- Option A: write the grants on `@server` only, as the table says. Cost: the five per-namespace purposes stop when the setting is `false`. No ADR change.
- Option B: NEW actions of the operator family for these purposes, which act inside namespaces for the housekeeping principals only. Cost: ADR 216 decision 7 says that a grant on `@server` "never gives an action inside a namespace", so this needs an amendment by the owner.
- Option C: the install writes the grants on `@server` for the installation-wide purposes, and the creation of each namespace writes one stored grant of each per-namespace role on that namespace (creator: the server, with the time). Cost: five grants for each namespace, visible in the access review. ADR 370 decision 3 says "The install step writes the grants of each housekeeping principal"; the namespace step needs an amendment by the owner.
- Recommendation: C. Every grant stays a visible stored row, and the sweeps work with the setting `false` (No magic). The cost is a larger access review and an ADR 370 amendment.
- 3 a.m. summary: without an answer, the purposes that clean up inside namespaces either stop in Cloud (A) or need a rule that ADR 216 forbids today (B).

**Q2. Which actions do the purposes with no action on main hold?** (security semantics; no owner ADR conflict)

`artifact-sweep`, `blob-sweep`, `checkpoint-sweep`, `name-lock-prune`, `temp-admin-expiry` and the reveal sweep of `agent-reaper` write audit actions that are not `identity.Action` values on main, so no existing action describes them.

- Option A: NEW narrow actions in the operator family: `server:sweep-artifacts`, `server:sweep-blobs`, `server:sweep-checkpoints`, `server:prune-name-locks`, `server:expire-temporary-administrators` and `server:sweep-agent-reveals`. Each role holds one. Cost: six new actions in `pkg/identity/identity.go` and `pkg/access/access.go`, and a change of the `server:` prefix with the rename of ADR 210 decision 7.
- Option B: reuse a wide action (for example `server:maintenance`, `server:manage-sign-in`, `server:manage-principals`). Cost: each role then holds more than its purpose needs.
- Recommendation: A, because least privilege is the point of ADR 370 decision 2.

**Q3. Does `job-reconcile` hold the start actions of the jobs it continues?** (security semantics)

`reconcileAction` returns the start action of the job ((read only) `pkg/job/reconcile.go` (line 451)). To authorize with it, the role holds `unit:run-plan`, `unit:run-apply`, `unit:run-destroy` and `unit:write-back`, with `job:cancel`.

- Option A: hold these five actions. Cost: the principal could start a destroy if someone holds its credential. Part 3 refuses a credential for it, and it is a stored grant that can be revoked.
- Option B: NEW action `job:reconcile`, which the authorizer accepts for the continuation of a job that a person started. Cost: one new action and a rule in the job service.
- Recommendation: B.

Questions considered and answered:

- Which purposes get a principal? The eleven purposes of the design document ("Purposes on main"). Task 0.1 checked the list on main on 17 November 2026.
- What happens to the seeded rows on a restore? The stage deletes them before it loads the backup, as it does for the installation row and the two policy rows; the backup holds its own rows. The existing step in `internal/store/sqlite/backup_stage_portable.go` and `internal/store/postgres/backup_stage_portable.go` is the precedent. Design decision 5.
- Does a housekeeping principal belong to a namespace? No. It is a principal of the installation. Its grants name the objects that its purpose touches.
- Who is the creator of the install grants? The install, recorded as the creator with the install time, as ADR 338 decision 1 did for the namespace grant.
- What about the existing namespaces? No installation exists, so no migration (ADR 322).

Drafted by the PM from ADR 370, as part 1 of 3 of the change that was named `housekeeping-principals` alone.
