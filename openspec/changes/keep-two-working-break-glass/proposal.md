---
version: 10
---

Issue: #623, #627, #633.

## Why

**Persona: site administrator.** A site administrator must recover an installation when the identity provider fails. Without two working break-glass accounts and a guard, one disable, delete or restore can lock every administrator out. The guard of the other writes is the change break-glass-floor-other-writes.

The user path this change must make work:

1. **Docs page.** The site administrator opens the guide for break-glass accounts and reads the floor and the guard.
2. **API.** The site administrator lists the break-glass accounts with the new read route and adds accounts with a passkey or a hardware key. The console and the CLI use the same routes.
3. **API.** The site administrator tries to disable or delete the last enabled break-glass account and gets 409 with the count.
4. **CLI.** The site administrator rotates a break-glass credential in one call. The new credential works before the old one stops.
5. **Server start.** The operator restores a backup. A restore that leaves fewer than two working paths is refused before the swap.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A disable or delete removes the last enabled break-glass account. | 409 with code principal.no-working-break-glass. The audit log records the refused change. | `GET /api/dustpan/v1/break-glass/accounts` |
| A restore leaves fewer than two working paths. | 409 with code principal.no-working-break-glass before the swap. | `GET /api/dustpan/v1/break-glass/accounts` |
| A guarded change comes without a fresh authentication. | 403 with code mfa.fresh-step-required. The text names the page that enrolls a factor. | `GET /api/dustpan/v1/break-glass/accounts` |
| A rotation names an account that is not a break-glass account or does not exist. | 404 with code break-glass.not-found. | `GET /api/dustpan/v1/break-glass/accounts` |

Evidence for the claims above:

- ADR 70 decisions 23 and 24 require two accounts and a guard that refuses a change that leaves fewer than two working paths. Proof: Pack ADR 70 decisions 23 and 24, and issue 623.
- ADR 306 decisions 3 and 6 stop disable and delete at the last enabled account. The sweep never disables a break-glass account on main, and it stays exempt. Proof: Pack ADR 306 decisions 3 and 6.
- The IP allowlist code is in internal/api/server/ip_allowlist.go and pkg/ipallow. Proof: The pack path list shows both as existing.
- The principal domain is in pkg/principal and the guides are in docs/guides. Proof: The pack path list shows both as existing.
- The passkey or hardware key requirement comes from decision 23 of ADR 70, and ADR 320 decision 23 repeats it. Proof: Pack issue 627 and ADR 320 decision 23 text: it signs in with a passkey or a hardware key only.
- Main already has a test that the inactivity sweep never disables a break-glass account. Proof: Pack main list: TestSweepNeverDisablesBreakGlass at pkg/accesstoken/accesstoken_test.go:1301.

## What Changes

- New route GET /api/dustpan/v1/break-glass/accounts: lists the break-glass accounts, the live count of working paths and the missing count. The route computes the count from the live rows at read time, so a write that disables an account, suspends its passkey or ends its grant shows in the next read. A break-glass account has no expiry field on main (`pkg/principal` has none), so no time-only case exists. Its action is server:read-principals and its object is @server (ServerObject). No new action exists.
- New route POST /api/dustpan/v1/break-glass/accounts: adds a break-glass account with a passkey or a hardware key, for first setup and for upgrade. It needs a fresh authentication. Its action is server:manage-principals and its object is @server (ServerObject). The guard never refuses it, because it raises the count, also when the count is zero.
- A count of working break-glass accounts in pkg/principal with the four properties of ADR 70 decision 24, and the passkey or hardware key of decision 23. On the hosted service it counts principals with the role admin on the namespace. The guard covers disable, delete and restore only. A revoke of that grant, a membership change and a move of a principal go to the change break-glass-floor-other-writes.
- A guard in the write transaction of disable, delete and restore. Disable and delete refuse when no enabled account would stay. The restore refuses when fewer than two working paths would stay. The three refusals use the one error code principal.no-working-break-glass. Each refusal writes an audit entry. Re-enable stays checked and a disable works in maintenance mode.
- The sweep stays exempt in SQL, as on main. For each break-glass account that the sweep skips, it writes one audit record: kind change, outcome success, scope @server, object the principal reference of the account, reason "break-glass account is exempt", and the caller fields of the sweep entry (the same entry shape that the sweep writes for a disabled account in pkg/accesstoken/service.go).
- New route POST /api/dustpan/v1/break-glass/accounts/{id}/rotate: atomic rotation of a break-glass credential.
- The restore check reads the same count on staged data, and the swap runs in a transaction that counts again.
- The guard uses the conditional SQL update of the disable and the delete: the statement changes the row only when another enabled account stays.
- Rotation is one new method of the passkey port, RotatePasskey in pkg/mfa/mfa.go, with the adapters in internal/store/sqlite/passkeys.go and internal/store/postgres/passkeys.go and a conformance case that two parallel rotations leave exactly one working credential.
- A guide section for the break-glass accounts, the guard and the floor.

Out of scope:

- The guard of the other writes: access Revoke, RemovePasskey and the IP allowlist writes (change break-glass-floor-other-writes, which depends on this change).
- The notice to the other administrators on a disable (change break-glass-disable-notice, which depends on this change).
- The first-setup gate on two break-glass accounts and the shared e2e first-setup step. No setup-finish flow exists on main, so a placeholder change owns both (change keep-two-break-glass-at-setup, which depends on this change).
- Typed reason, alert and session limit of break-glass sign-in, and the per-use review text (change break-glass-sign-in-typed).
- Reach recheck and act-as edges, and their guide text (change check-reach-when-grant-membership).
- Group trust switches and protected groups, and their guide text (change let-group-trust-identity-provider).
- The setting for the restore of access tables (change add-setting-lets-server-restore).
- Signed-request recovery route (change signed-request-recovery).
- The change of the fresh-authentication window to 0 (change fresh-authentication-window-0-turns).

Depends on:

- add-setting-lets-server-restore
- fresh-authentication-window-0-turns

## Usability

Where the path still costs the person time or surprises them:

- A refused change costs one round trip. The 409 text names the live count and the route that lists the accounts.
- An installation with one account sees the missing count in the list route, and the add route names the passkey enrollment step.
- A person with no factor gets a refusal that names the enrollment page.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The passkey port removes a passkey in its own transaction. This change adds the rotation method next to it. (`pkg/mfa/mfa.go:227 (func RemovePasskey of PasskeyStore)`)
- The service removes a passkey of the caller. The rotation service method sits next to it. (`internal/mfasvc/service.go:553 (func RemovePasskey)`)
- A test shows that the inactivity sweep never disables a break-glass account. This change keeps that rule and adds one audit record for each skipped account. (`pkg/accesstoken/accesstoken_test.go:1301 (func TestSweepNeverDisablesBreakGlass)`)
- A test shows that the inactivity sweep disables inactive tokens and accounts. (`pkg/accesstoken/accesstoken_test.go:1248 (func TestSweepDisablesInactiveTokensAndAccounts)`)
- A temporary administrator test exists for the expiry of recovery access. (`pkg/dustpan/tempadmin_test.go:85 (func TestExpiredTempAdminStaysDenied)`)
- Restore error codes exist on main. (`internal/errcode/catalog_restore.go:7 (error code restore.blobs-missing)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P19)**, Item 17: the break-glass floor: "the PM reading in build. Disable, delete and sweep stop at the last enabled account (ADR 306 d3); every other change keeps the two-working-paths floor (ADR 70 d24); first setup needs two. Reason: the stricter floor wherever the newer owner text is silent; owner ADRs 70 and 306 both kept."
- **Owner decision, 13 Nov 2026 (queue P21)**, Item 19: #2924 installation-wide data role and protected groups: "(a) such a group is protected. Reason: write access to every state is the highest-impact power; cloud providers keep org-wide write roles out of automatic group sync. ADR 70 d13 (owner) read broadly; text waits."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 23 (changed), 24 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13; ADR 369 changes decision 9. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 370, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354, 369.
- Quote: "Each installation has break-glass accounts, the hosted service too. An installation has at least two break-glass accounts. Each one belongs to one named person. No account depends on the identity provider. Each account s"
- Quote: "A guard keeps two working paths. The first setup cannot finish without two break-glass accounts. The server refuses each change that leaves fewer than two working paths, a restore included. It counts only an account that"
- Effect: Decision 24 gives the four properties of a working account. Decision 23 gives the passkey or hardware key rule. The restore keeps the floor of two. The other lowering writes keep it in the change break-glass-floor-other-writes. First setup needs two accounts.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Delete and sweep (owner, 2026-10-20, accepted as written for now). Delete and sweep follow the same "not the last one" rule. This changes ADR 70 decisions 25 and 26. The sweep may disable or delete a break-glass principal."
- Effect: Disable and delete use the rule that one enabled account must stay, with 409 in the write transaction. Decision 6 says the sweep may disable or delete a break-glass principal. This change does not use that permission: the sweep stays exempt, and a sweep that is exempt never leaves fewer enabled accounts than the rule needs. Each skipped account gets one audit record that names the sweep, the account and the reason. The disable notice is the change break-glass-disable-notice.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 13 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Break-glass accounts of a namespace. On OpenDustpan Cloud, the break-glass accounts of ADR 70 decision 23 are principals of the namespace with the role `admin` on it, not holders of `account-administrator` on the install"
- Effect: On the hosted service the count and the restore check use the principals of the namespace with the role admin, for that namespace. On a self-hosted installation they use the installation.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (not owner-tagged).

- Decisions that apply: 23 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "A break-glass account has no lockout by name (PM). It signs in with a passkey or a hardware key only (ADR 70 decision 23), so the counts of decision 19 do not apply to it."
- Effect: The count accepts a passkey or a hardware key. A hardware key counts as a passkey.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Rotation, add and the guarded changes of break-glass accounts use the one fresh-authentication window.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which threshold applies to which change? Owner decision, 13 Nov 2026 (queue P19), provisional and built as the reading: disable and delete stop at the last enabled account (ADR 306 d3), and a restore keeps the floor of two working paths (ADR 70 d24). The sweep stays exempt. The other writes keep the floor of two in the change break-glass-floor-other-writes. First setup needs two, and the change keep-two-break-glass-at-setup builds that part.
- Does a disable that leaves one enabled account break ADR 70 decision 24? No. ADR 306 decision 3 changes decision 24 for disable and delete. The change passes and the answer carries the live count.
- Does ADR 70 decision 25, no job disables a break-glass account, conflict with the sweep? ADR 306 decision 6 says the sweep may disable a break-glass principal. It is permission, not a duty. The sweep stays exempt, as on main, and writes one audit record for each account that it skips.
- What counts as a working account? ADR 70 decision 24 gives enabled, not expired, not blocked by a policy, not dependent on the identity provider. The count on main (`pkg/principal/breakglass_count.go:30 (func CountWorkingBreakGlass)`) has no expiry term, and the change adds none. ADR 70 decision 23 and ADR 320 decision 23 give the passkey or hardware key.
- Which source is authoritative for the count? The live rows are the only source. No stored setup list exists. The list route computes the count from the live rows at read time, so it never shows a stored number. An account has no expiry field, so time alone does not lower the count.
- What does a person with no enrolled factor do? ADR 301 decision 4 and ADR 227 decision 2 apply: 403 with mfa.fresh-step-required and a text that names the enrollment page.
- Which scope does the count use? ADR 216 decision 13: the namespace on the hosted service, the installation when self-hosted.
- Can an installation below the floor add an account? Yes. A change that raises the count or does not lower it is never refused, also at zero accounts.
- Does the guide cover protected groups and reach? No. Other changes own that text. Owner decision, 13 Nov 2026 (queue P21) belongs to let-group-trust-identity-provider.
- Where does the first-setup gate live? Not in this change: no setup-finish flow exists on main. The change keep-two-break-glass-at-setup owns the gate, its test and the shared e2e first-setup step.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
