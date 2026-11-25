---
version: 1
---

Issue: #623.

## Why

**Persona: site administrator.** A site administrator must recover an installation when the identity provider fails. The change keep-two-working-break-glass guards disable, delete and restore. Three other writes can also take a break-glass account out of work and leave fewer than two working paths: a revoke of an access grant, a removal of a passkey, and a write of an IP allowlist. This change puts the same guard on them.

The user path this change must make work:

1. **API.** The site administrator revokes a grant, removes a passkey or writes an IP allowlist, as before, when two working break-glass accounts stay.
2. **API.** The site administrator tries one of these writes and it would leave fewer than two working paths. The server answers 409 with the live count and does not change the data.
3. **Docs page.** The site administrator reads in the guide for break-glass accounts which writes keep the floor of two.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A revoke of a grant lowers the count below two. | 409 with code principal.no-working-break-glass. The audit log records the refused change. | `GET /api/dustpan/v1/break-glass/accounts` |
| A removal of a passkey lowers the count below two. | 409 with code principal.no-working-break-glass. | `GET /api/dustpan/v1/break-glass/accounts` |
| A write of an IP allowlist lowers the count below two. | 409 with code principal.no-working-break-glass. | `GET /api/dustpan/v1/break-glass/accounts` |

Evidence for the claims above:

- ADR 70 decision 24 requires that the server refuses each change that leaves fewer than two working paths. Proof: Pack ADR 70 decision 24, and issue 623.
- Owner decision, 13 Nov 2026 (queue P19), item 17: every change other than disable, delete and the sweep keeps the floor of two. Proof: the quote in "Owner decisions that apply".
- The access store already refuses the revoke of the last site administrator. Proof: `pkg/access/access.go:538 (func RevokeGrant of the access Store)` returns ErrLastSiteAdministrator.
- The count function and the floor constant exist on main. Proof: `pkg/principal/breakglass_count.go:18 (const FloorWorkingPaths)`.
- The sentinel ErrNoWorkingBreakGlass exists on main and the restore uses it. Proof: `pkg/backup/restore_service.go:849 (list of the sentinels that the restore returns)`.

## What Changes

- A guard in the write transaction of three writes: `RevokeGrant` of the access store, `RemovePasskey` of the passkey store, and `SetNamespaceList` of the IP allowlist store. Each write counts the working break-glass accounts in its own transaction, after its own change, with the count function of the change keep-two-working-break-glass.
- The rule is the one of ADR 70 decision 24: a write is refused when it lowers the count and the count after it is below FloorWorkingPaths (2). A write that does not lower the count, or raises it, is never refused, also when the count is already below two. This lets an installation recover.
- The refusal returns ErrNoWorkingBreakGlass. The adapters map it to 409 with the code principal.no-working-break-glass, the one code of the change keep-two-working-break-glass. No new code, action or route exists.
- Each refusal writes an audit entry in the same transaction as the check, with the action of the route and the object of the write.
- The grant that makes a break-glass account work counts. On the hosted service it is the role admin on the namespace; on a self-hosted installation it is the site administrator grant. A revoke of any other grant does not change the count and is never refused by this guard.
- The guide for break-glass accounts lists the writes that keep the floor.

Out of scope:

- The guard of disable, delete and restore, the count function, the set type and the sweep record (change keep-two-working-break-glass, which this change depends on).
- The notice to the other administrators on a disable (change break-glass-disable-notice).
- The first-setup gate (change keep-two-break-glass-at-setup).
- An expiry field or a policy-block field of a break-glass account. No such field exists on main, so this change has no scenario for them.

Depends on:

- keep-two-working-break-glass

## Usability

Where the path still costs the person time or surprises them:

- A refused write costs one round trip. The 409 text names the live count and the route that lists the accounts.
- A write that does not touch a break-glass account never sees the guard, so the usual routes stay as fast as on main.
- The refused passkey removal names the credential that would leave the account without a working path.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The access store revokes a grant in one transaction and refuses the end of the last site administrator. This change adds the floor of two for the grants of break-glass accounts. (`pkg/access/access.go:538 (func RevokeGrant of the access Store)`)
- The SQL adapters of that method. (`internal/store/sqlite/access.go:250 (func RevokeGrant)`, `internal/store/postgres/access.go:244 (func RevokeGrant)`)
- The passkey port removes a passkey in its own transaction. (`pkg/mfa/mfa.go:227 (func RemovePasskey of PasskeyStore)`)
- The shared body of the removal and its two adapters. (`internal/store/passkeystore/passkeystore.go:385 (func RemovePasskey)`, `internal/store/sqlite/passkeys.go:87 (func RemovePasskey)`, `internal/store/postgres/passkeys.go:115 (func RemovePasskey)`)
- The IP allowlist port replaces the list of a namespace in the transaction that checks the fence and appends the audit entry. (`pkg/ipallow/ipallow.go:36 (func SetNamespaceList of Store)`)
- The adapters and the HTTP code of the list. (`internal/store/sqlite/ipallow.go`, `internal/store/postgres/ipallow.go`, `internal/api/server/ip_allowlist.go:160 (func writeIPAllowlist)`, `internal/api/server/ip_allowlist.go:198 (func removeIPAllowlist)`)
- The HTTP code maps ErrLastSiteAdministrator and the IP allowlist errors to their status. (`internal/api/server/access.go:880 (case ErrLastSiteAdministrator)`, `internal/api/server/ip_allowlist.go:230 (func classifyIPAllowlist)`)
- The count function says that an installation-level IP allowlist must join the count when one exists, and that the namespace lists do not apply to installation sign-in. (`pkg/principal/breakglass_count.go:21 (interface WorkingBreakGlassCounter)`)
- The sentinel exists. (`pkg/backup/restore_service.go:849 (list of the sentinels that the restore returns)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P19)**, Item 17: the break-glass floor: "the PM reading in build. Disable, delete and sweep stop at the last enabled account (ADR 306 d3); every other change keeps the two-working-paths floor (ADR 70 d24); first setup needs two. Reason: the stricter floor wherever the newer owner text is silent; owner ADRs 70 and 306 both kept."

## ADR check

Each quote is copied from the file in `docs/adr/`.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 24 (changed).
- Changed by: ADR 216 changes decisions 11, 23, 24 and 28; ADR 306 changes decisions 24, 25 and 26. Chain to the end: see the change keep-two-working-break-glass.
- Current rule: this ADR, with ADR 216 and ADR 306.
- Quote: "A guard keeps two working paths. The first setup cannot finish without two break-glass accounts. The server refuses each change that leaves fewer than two working paths, a restore included. It counts only an account that"
- Effect: Revoke, RemovePasskey and the IP allowlist write keep the floor of two. ADR 306 decision 3 lowers the floor to one only for disable, delete and the sweep.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Delete and sweep (owner, 2026-10-20, accepted as written for now). Delete and sweep follow the same "not the last one" rule."
- Effect: The lower floor applies to disable, delete and the sweep only. This change guards other writes with the floor of two.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 13 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Break-glass accounts of a namespace. On OpenDustpan Cloud, the break-glass accounts of ADR 70 decision 23 are principals of the namespace with the role `admin` on it, not holders of `account-administrator` on the install"
- Effect: On the hosted service the revoke of the role admin of a break-glass principal lowers the count of its namespace.
- Conflict: none.

**ADR 250: Defense in depth** (not owner-tagged).

- Decisions that apply: the layers of the guard.
- Effect: The guard has its own test for the port adapter, the service and the HTTP handler.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which floor applies? The floor of two, from Owner decision, 13 Nov 2026 (queue P19), item 17: every change other than disable, delete and the sweep.
- Does a write that is refused need a new error code? No. The one code principal.no-working-break-glass of the change keep-two-working-break-glass answers 409 for all guarded writes.
- Does an IP allowlist lower the count today? The namespace lists do not apply to installation sign-in, so on a self-hosted installation the count does not change by a list write. The guard still runs the count in the write transaction, so that a later count that reads the lists needs no new write path. On the hosted service the count function decides whether a list blocks the accounts of its namespace.
- Which expiry and policy-block fields exist? None on main. The change has no scenario for them. A change that adds such a field adds its own scenario.
- Is the sweep covered? No. The sweep stays exempt in the change keep-two-working-break-glass.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs.
