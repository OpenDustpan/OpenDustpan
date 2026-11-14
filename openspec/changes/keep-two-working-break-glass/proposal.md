---
version: 2
---

Issue: #623, #627, #633.

## Why

**Persona: site administrator.** A site administrator must recover an installation when the identity provider fails. Without two working break-glass accounts and a guard, one disable, revoke, passkey removal or restore can lock every administrator out.

The user path this change must make work:

1. **Docs page.** The site administrator opens the guide for break-glass accounts and reads the setup rule and the guard.
2. **API.** The site administrator lists the break-glass accounts with the new read route and adds accounts with a passkey or a hardware key. The console and the CLI use the same routes.
3. **API.** The site administrator tries a change that lowers the count of working paths below the floor and gets 409 with the count.
4. **CLI.** The site administrator rotates a break-glass credential in one call. The new credential works before the old one stops.
5. **Server start.** The operator restores a backup. A restore that leaves fewer than two working paths is refused before the swap.
6. **E2e.** The shared e2e first setup makes two break-glass accounts, so each other e2e step still starts from a finished setup.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A revoke, credential change, passkey removal, expiry change, policy block or IP allowlist change leaves fewer than two working break-glass paths. | 409 with code break-glass.floor and text that names the live count. The audit log records the refused change. | `GET /api/dustpan/v1/break-glass/accounts` |
| A disable, delete or sweep removes the last enabled break-glass account. | 409 with code break-glass.last-enabled. The audit log records the refused change. | `GET /api/dustpan/v1/break-glass/accounts` |
| First-setup finish with fewer than two break-glass accounts. | 409 with code break-glass.setup-incomplete and the missing count. | `GET /api/dustpan/v1/break-glass/accounts` |
| A restore leaves fewer than two working paths. | 409 with code break-glass.restore-refused before the swap. | `GET /api/dustpan/v1/break-glass/accounts` |
| A concurrent write holds the lock on the break-glass set past the lock timeout. | 409 with code break-glass.busy. No change is written. A retry is safe. | `GET /api/dustpan/v1/break-glass/accounts` |
| A guarded change comes without a fresh authentication. | 403 with code mfa.fresh-step-required. The text names the page that enrolls a factor. | `GET /api/dustpan/v1/break-glass/accounts` |
| A rotation names an account that is not a break-glass account or does not exist. | 404 with code break-glass.not-found. | `GET /api/dustpan/v1/break-glass/accounts` |

Evidence for the claims above:

- ADR 70 decisions 23 and 24 require two accounts and a guard that refuses a change that leaves fewer than two working paths. Proof: Pack ADR 70 decisions 23 and 24, and issue 623.
- ADR 306 decisions 3 and 6 stop disable, delete and sweep at the last enabled account. Proof: Pack ADR 306 decisions 3 and 6.
- The IP allowlist code is in internal/api/server/ip_allowlist.go and pkg/ipallow. Proof: The pack path list shows both as existing.
- The principal domain is in pkg/principal and the guides are in docs/guides. Proof: The pack path list shows both as existing.
- The passkey or hardware key requirement comes from decision 23 of ADR 70, and ADR 320 decision 23 repeats it. Proof: Pack issue 627 and ADR 320 decision 23 text: it signs in with a passkey or a hardware key only.
- Main already has a test that the inactivity sweep never disables a break-glass account. Proof: Pack main list: TestSweepNeverDisablesBreakGlass at pkg/accesstoken/accesstoken_test.go:1270.

## What Changes

- New route GET /api/dustpan/v1/break-glass/accounts: lists the break-glass accounts, the live count of working paths and the missing count. The count also drops by time alone when an account expires, and the route shows it. Its action is server:read-break-glass and its object is @server.
- New route POST /api/dustpan/v1/break-glass/accounts: adds a break-glass account with a passkey or a hardware key, for first setup and for upgrade. It needs a fresh authentication. The guard never refuses it, because it raises the count, also when the count is zero.
- A count of working break-glass accounts in pkg/principal with the four properties of ADR 70 decision 24, and the passkey or hardware key of decision 23. On the hosted service it counts principals with the role admin on the namespace. A revoke of that grant, a membership change or a move of a principal runs the guard.
- A guard in the write transaction of each covered change. Disable, delete and sweep refuse when no enabled account would stay. Each disable writes an audit entry and notifies the other administrators. Re-enable stays checked and a disable works in maintenance mode. Every other lowering change refuses when it lowers the count below two. Each refusal writes an audit entry.
- New route POST /api/dustpan/v1/break-glass/accounts/{id}/rotate: atomic rotation of a break-glass credential.
- A first-setup gate on two break-glass accounts with a passkey or a hardware key. The restore check reads the same count on staged data, and the swap runs in a transaction that counts again.
- The lock timeout is an installation setting, breakGlassLockSeconds, with a default of 5 and its effective value shown in the settings resource.
- The shared e2e first-setup step makes two break-glass accounts.
- A guide section for the break-glass accounts, the guard and the setup rule.

Out of scope:

- Typed reason, alert and session limit of break-glass sign-in, and the per-use review text (change break-glass-sign-in-typed).
- Reach recheck and act-as edges, and their guide text (change check-reach-when-grant-membership).
- Group trust switches and protected groups, and their guide text (change let-group-trust-identity-provider).
- The setting for the restore of access tables (change add-setting-lets-server-restore).
- Mode 2 recovery route (change break-glass-mode-2).
- The change of the fresh-authentication window to 0 (change fresh-authentication-window-0-turns).

Depends on:

- add-setting-lets-server-restore
- fresh-authentication-window-0-turns
- `add-setting-lets-server-restore` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused change costs one round trip. The 409 text names the live count and the route that lists the accounts.
- Setup with one account stops at finish. The list route names the missing count and the passkey enrollment step.
- A person with no factor gets a refusal that names the enrollment page.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A test shows that the inactivity sweep never disables a break-glass account. This change replaces that rule with the last-enabled rule for the sweep. (`pkg/accesstoken/accesstoken_test.go:1270 (func TestSweepNeverDisablesBreakGlass)`)
- A test shows that the inactivity sweep disables inactive tokens and accounts. (`pkg/accesstoken/accesstoken_test.go:1217 (func TestSweepDisablesInactiveTokensAndAccounts)`)
- A temporary administrator test exists for the expiry of recovery access. (`pkg/dustpan/tempadmin_test.go:84 (func TestExpiredTempAdminStaysDenied)`)
- Restore error codes exist on main. (`internal/errcode/catalog_restore.go:7 (error code restore.blobs-missing)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P19)**, Item 17: the break-glass floor: "the PM reading in build. Disable, delete and sweep stop at the last enabled account (ADR 306 d3); every other change keeps the two-working-paths floor (ADR 70 d24); first setup needs two. Reason: the stricter floor wherever the newer owner text is silent; owner ADRs 70 and 306 both kept."
- **Owner decision, 13 Nov 2026 (queue P21)**, Item 19: #2924 installation-wide data role and protected groups: "(a) such a group is protected. Reason: write access to every state is the highest-impact power; cloud providers keep org-wide write roles out of automatic group sync. ADR 70 d13 (owner) read broadly; text waits."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 23 (changed), 24 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "Each installation has break-glass accounts, the hosted service too. An installation has at least two break-glass accounts. Each one belongs to one named person. No account depends on the identity provider. Each account s"
- Quote: "A guard keeps two working paths. The first setup cannot finish without two break-glass accounts. The server refuses each change that leaves fewer than two working paths, a restore included. It counts only an account that"
- Effect: Decision 24 gives the four properties of a working account. Decision 23 gives the passkey or hardware key rule. Every lowering change except disable, delete and sweep keeps the floor of two. First setup needs two accounts.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Delete and sweep (owner, 2026-10-20, accepted as written for now). Delete and sweep follow the same "not the last one" rule. This changes ADR 70 decisions 25 and 26. The sweep may disable or delete a break-glass principal."
- Effect: Disable, delete and sweep use the rule that one enabled account must stay, with 409 in the write transaction. Each disable is audited and notifies the other administrators. Each sweep action writes an audit record that names the sweep, the account and the reason.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 13 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Break-glass accounts of a namespace. On OpenDustpan Cloud, the break-glass accounts of ADR 70 decision 23 are principals of the namespace with the role `admin` on it, not holders of `account-administrator` on the install"
- Effect: On the hosted service the count, the setup and the restore check use the principals of the namespace with the role admin, for that namespace. On a self-hosted installation they use the installation.
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

Not spec-ready: a code check fails: threat 1 (A caller without a fresh authentication ): no guard and hostile test for encode or contain, limit; a code check fails: threat 2 (A disable, delete, sweep, revoke or rest): no guard and hostile test for boundary check, encode or contain; a code check fails: threat 3 (A restored backup brings fewer break-gla): no guard and hostile test for encode or contain, limit, authorize at point of use; a code check fails: and 1 more.

Questions considered and answered:

- Which threshold applies to which change? Owner decision, 13 Nov 2026 (queue P19), provisional and built as the reading: disable, delete and sweep stop at the last enabled account (ADR 306 d3, d6). Every other change keeps the floor of two working paths (ADR 70 d24). First setup needs two.
- Does a disable that leaves one enabled account break ADR 70 decision 24? No. ADR 306 decision 3 changes decision 24 for disable, delete and sweep. The change passes and the answer carries the live count.
- Does ADR 70 decision 25, no job disables a break-glass account, conflict with the sweep? ADR 306 decision 6 changes decisions 25 and 26 for the sweep. The sweep follows the not-the-last-one rule and writes an audit record.
- What counts as a working account? ADR 70 decision 24 gives enabled, not expired, not blocked by a policy, not dependent on the identity provider. ADR 70 decision 23 and ADR 320 decision 23 give the passkey or hardware key.
- Which source is authoritative for the count? The live rows are the only source. No stored setup list exists. Time alone can lower the count, so the list route computes it at read time.
- What does a person with no enrolled factor do? ADR 301 decision 4 and ADR 227 decision 2 apply: 403 with mfa.fresh-step-required and a text that names the enrollment page.
- Which scope does the count use? ADR 216 decision 13: the namespace on the hosted service, the installation when self-hosted.
- Can an installation below the floor add an account? Yes. A change that raises the count or does not lower it is never refused, also at zero accounts.
- Does the guide cover protected groups and reach? No. Other changes own that text. Owner decision, 13 Nov 2026 (queue P21) belongs to let-group-trust-identity-provider.
- Does the stricter first setup break the e2e suites of other steps? It would, so a task updates the shared e2e first-setup fixture to make two accounts.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
