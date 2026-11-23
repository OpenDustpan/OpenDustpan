---
version: 4
---

Issue: #3289.

Decision: ADR 370. Part 1 of 3 of the housekeeping principals. Part 2 is `housekeeping-principals-sweeps`. Part 3 is `housekeeping-principals`, which keeps the name of the whole work, so each change that waits for "housekeeping-principals" waits for all three parts. This part touches no file of the access token, identity and sign-in packages.

## Why

**Persona: operator.** An operator reads the access review of an installation. Today no principal exists for the housekeeping of the server (the sweeps, the retention, the expiry of temporary administrators), and no stored grant says what it may do. The operator needs each housekeeping purpose to exist as a named principal with stored grants that the operator can list and read. This part makes the principals and their grants. Part 2 and part 3 make each sweep run as its principal.

The user path this part must make work:

1. **API.** The operator lists the principals of the installation: the existing route `GET /api/server/principals?kind=service&source=builtin` (NEW `source` filter and NEW `builtin` source). The answer lists one housekeeping principal for each purpose, for example `housekeeping:artifact-sweep`, with its stable ID.
2. **API.** The operator lists the grants that one housekeeping principal holds: the existing route `GET /api/server/access-review?subject=principal:<id>`. Each row has a role, an object, a creator (the install) and a time. The route `GET /api/server/principals/{principal}/grants` lists the grants on the principal as an object, so it lists none of these.
3. **API.** The operator creates a namespace with the existing route `POST /api/server/namespaces`. The access review of the namespace with `subject=principal:<id>` of `housekeeping:history-retention` then lists a grant of its role on the new namespace, with the server as the creator and the creation time. The same holds for the four other roles that act inside namespaces.
4. **Docs page.** The `source` row of the principal fields in `docs/guides/principals-and-groups.md` lists the `builtin` value.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The `source` filter has a value that is not valid. | 400, as for the `kind` filter. | `GET /api/server/principals?kind=service&source=builtin` |

Evidence for the claims above:

- The list route has the `kind` filter and no `source` filter. Proof: the first three facts of "What main already does".

## What Changes

- NEW: one built-in service principal for each housekeeping purpose, made at the install, with the source `builtin`.
- NEW: the install step writes the grants of the housekeeping principals that act on the installation as stored grants on `@server`, with the install as the creator and the install time. A role for each purpose holds only the actions that the purpose needs. The role name and the exact actions of each purpose are in the table "Roles of the housekeeping principals" of the design document, and the constants, descriptions and action lists are NEW in `pkg/access/roles.go`.
- NEW: each namespace creation writes one stored grant on the new namespace for each of the five roles that act inside namespaces (`housekeeping-agent-run-reaper`, `housekeeping-history-retention`, `housekeeping-issuer-rotation`, `housekeeping-job-reconcile`, `housekeeping-saved-view-cleanup`), in the same transaction as the namespace, with the server as the creator and the creation time (owner, 2026-11-22). Each grant has its own audit entry in the chain of the new namespace.
- NEW: the purpose `agent-reaper` holds two roles, so that no role granted on `@server` holds a namespace action (owner, 2026-11-23, "Split the role"): the installation role `housekeeping-agent-reveal-sweep` holds only `server:sweep-agent-reveals` and is granted on `@server` at the install, and the namespace role `housekeeping-agent-run-reaper` holds only `namespace:manage-agent-runs` and is granted on each namespace at its creation. A revoke of the namespace grant then denies the action on that namespace.
- NEW: seven actions. Six narrow actions of the operator family, one for each sweep that has no action on main: `server:sweep-artifacts`, `server:sweep-blobs`, `server:sweep-checkpoints`, `server:prune-name-locks`, `server:expire-temporary-administrators` and `server:sweep-agent-reveals`. One action `job:reconcile`, which the job service checks for the continuation of a job (owner, 2026-11-22). They go in `pkg/identity/identity.go` and in the `families` map of `pkg/access/access.go`.
- NEW: the classification of `unit:redact` and `namespace:manage-agent-runs` in the `families` map as `FamilyData`. Both are in `pkg/identity/identity.go` and not in the map on main.
- NEW: the `source` filter of `GET /api/server/principals` and the `builtin` source. The other routes exist on main.
- NEW: a portable restore clears the seeded principals and grants of the new baseline before it loads the backup, so the restored installation has the rows of the backup.
- Test side only: a test that the revoke of the namespace grant of `housekeeping-agent-run-reaper` denies `namespace:manage-agent-runs` on that namespace, a guard test that no role granted on `@server` holds a namespace action, a correction of the comment in `internal/api/server/review_test.go`, and the shared suites of principals, namespace principals and access expect the seeded rows in their totals, page orders and review counts, and the five grants of each created namespace.
- Update `GLOSSARY.md`: add "Housekeeping principal".

Out of scope:

- A filter or a hidden rule that removes the seeded rows from a list: the seed is part of the empty installation.
- The sweeps that act as their principals, and the check of `job:reconcile` in the job service (part 2 and part 3).
- A refusal of a credential or of a grant for a housekeeping principal (part 3).
- The namespace service principal: the first trigger change removes it with the run-as principal that replaces it.
- A route to make a new housekeeping principal. The set is fixed by the code, and only their grants are data.

Depends on:

- none

Builds on (not a change):

- ADR 370 on main (pull request #3058)

## Usability

Where the path still costs the person time or surprises them:

- Each created namespace has five more grants in its access review, one for each role that acts inside namespaces. They are stored grants that an operator can list and revoke, and they are the price of a sweep that works when `access.installationGrantsReachNamespaces` is `false`. The docs page of part 3 explains them.
- Until part 3 lands, nothing refuses a person who asks for a grant of any role to a housekeeping principal. The principals exist and are listed, and no sweep uses them yet. The docs page for the operator comes with part 3.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The list route reads the `kind` filter and no `source` filter. (`internal/api/server/principals.go:793 (func listPrincipals)`)
- The store port takes the kind and no source. (`pkg/principal/store.go:58 (Store.ListPrincipals)`)
- The service parses the kind. (`pkg/principal/service.go:200 (Service.ListPrincipals)`)
- The source constants stop at `ldap`, `scim`, `oidc` and `oauth` next to `local`. (`pkg/principal/principal.go:132`)
- The source check refuses any other value. (`pkg/principal/principal.go:649 (func validateSource)`)
- The namespace creation stores the namespace and one grant of its creator, and no other grant. (`pkg/access/service.go:595 (Service.CreateNamespace)`, `internal/store/sqlite/access.go:479 (Store.CreateNamespaceWithGrant)`, `internal/store/postgres/access.go:439`)
- `unit:redact` and `namespace:manage-agent-runs` are not in the `families` map. (`pkg/access/access.go:119`, checked by `rg 'ActionUnitRedact|ActionNamespaceManageAgentRuns' pkg/access/access.go`, which prints nothing)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 370: Housekeeping principals replace the namespace service principal** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 3 (current, amended by this work), 4 (current).
- Changed by: ADR 394 changes decision 3 (owner, 2026-11-22). Chain to the end: ADR 394.
- Current rule: this ADR, with ADR 394.
- Quote: "Each housekeeping purpose has its own built-in service principal (owner, 2026-11-14)."
- Quote of decision 3 after the amendment: "The install step and each namespace creation write the grants of each housekeeping principal as stored grants, with a creator and a time."
- Quote: "A revoke of the grant of one housekeeping principal stops only its own work, and the server writes the denial to the audit log."
- Effect: This part builds the principals of decision 2 and the stored grants of decision 3 at the install and at each namespace creation, in the baseline (decision 4). Decision 3 revoke and denial are built by part 2 and part 3. Decision 1 is built by the first trigger change.
- Conflict: none. The owner decided the amendment on 22 November 2026 (Q1 below). The same pull request records the amendment: ADR 394 and the edit of decision 3 with its changelog entry.

**ADR 216: Access scopes and the reach of the installation grant** (owner-tagged: decision 7).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "With `true`, a grant on `@server` reaches each namespace, as ADR 52 decision 5 says. With `false`, a grant on `@server` gives only the actions of the operator scope. It never gives an action inside a namespace."
- Effect: A grant of a housekeeping role on `@server` would reach a namespace only when the setting is `true`. This change does not rely on that: the five roles that act inside namespaces get a grant on each namespace (decision of Q1, owner, 2026-11-22). The words of decision 7 stay as they are, and no action of the operator family acts inside a namespace.
- Conflict: none.

**ADR 52: Namespaces, collections and units** (decision 5, the grant chain unit, collection, namespace, `@server`).

- Quote: "a grant on `@server` reaches each namespace" (as ADR 216 decision 7 cites it).
- Effect: The rows of the role table that say "Each namespace" use a grant on the namespace, the second link of this chain, so they do not depend on the setting of ADR 216 decision 7.
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

- How does a housekeeping principal reach the namespaces? Each namespace creation writes one stored grant on the namespace for each of the five roles `housekeeping-agent-run-reaper`, `housekeeping-history-retention`, `housekeeping-issuer-rotation`, `housekeeping-job-reconcile` and `housekeeping-saved-view-cleanup`, with the server as the creator and the creation time (owner, 2026-11-22, option C). The owner decided on 23 November 2026 that the `agent-reaper` purpose holds two roles ("Split the role", ADR 394 decision 4): the grant of `@server` never holds a namespace action. Options A (a grant on `@server` only, so the five stop when the setting is `false`) and B (operator actions that act inside namespaces, which needs an amendment of ADR 216 decision 7) were not chosen. This needs the amendment of ADR 370 decision 3 to "the install step and each namespace creation", recorded in ADR 394 (owner, 2026-11-22). Design decisions 2 and 6.
- Which actions do the purposes with no action on main hold? Six NEW narrow actions of the operator family, one for each sweep, with the names of the role table (owner, 2026-11-22, option A). Option B (a wide action such as `server:maintenance`) was not chosen. Design decision 7.
- Does `job-reconcile` hold the start actions of the jobs it continues? No. It holds a NEW action `job:reconcile`, which the job service checks for the continuation of a job that a person started (owner, 2026-11-22, option B). Part 2 adds the check in the job service. Design decision 8.
- Which purposes get a principal? The eleven purposes of the design document ("Purposes on main"). Task 0.1 checked the list on main on 17 November 2026.
- What happens to the seeded rows on a restore? The stage deletes them before it loads the backup, as it does for the installation row and the two policy rows; the backup holds its own rows. The existing step in `internal/store/sqlite/backup_stage_portable.go` and `internal/store/postgres/backup_stage_portable.go` is the precedent. Design decision 5.
- Does a housekeeping principal belong to a namespace? No. It is a principal of the installation. Its grants name the objects that its purpose touches.
- Who is the creator of the install grants? The install, recorded as the creator with the install time, as ADR 338 decision 1 did for the namespace grant. The creator of a namespace grant is the server, and the audit entry of the grant names the person who created the namespace.
- What about the existing namespaces? No installation exists, so no migration (ADR 322).
- Are `unit:redact` and `namespace:manage-agent-runs` classified? No, on main (`rg` check in "What main already does"). Task 2.1 classifies them as `FamilyData`, which Q1 no longer puts in doubt: the roles hold them through grants on namespaces.

Drafted by the PM from ADR 370, as part 1 of 3 of the change that was named `housekeeping-principals` alone.
