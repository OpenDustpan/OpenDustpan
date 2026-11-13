Issue: #623, #627, #633.

## Why

**Persona: site administrator.** A site administrator must recover an installation when the identity provider fails. Without two working break-glass accounts and a guard, one disable, revoke, passkey removal or restore can lock every administrator out.

The user path this change must make work:

1. **Docs page.** The site administrator opens the guide for break-glass accounts and reads the setup rule and the guard.
2. **API.** The site administrator lists the break-glass accounts and adds accounts with a passkey or a hardware key. The console and the CLI use the same routes.
3. **API.** The site administrator tries a change that lowers the count of working paths below the floor and gets 409 with the count.
4. **CLI.** The site administrator rotates a break-glass credential in one call. The new credential works before the old one stops.
5. **Server start.** The operator restores a backup. A restore that leaves fewer than two working paths is refused before the swap.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A revoke, credential change, passkey removal, expiry change, policy block or IP allowlist change leaves fewer than two working break-glass paths. | 409 with code break-glass.floor and text that names the live count. | `GET /api/dustpan/v1/break-glass-accounts` |
| A disable, delete or sweep removes the last enabled break-glass account. | 409 with code break-glass.last-enabled. The audit log records the refused change. | `GET /api/dustpan/v1/break-glass-accounts` |
| First-setup finish with fewer than two break-glass accounts. | 409 with code break-glass.setup-incomplete and the missing count. | `GET /api/dustpan/v1/setup` |
| A restore leaves fewer than two working paths. | 409 with code break-glass.restore-refused before the swap. | `GET /api/dustpan/v1/backups` |
| A concurrent write holds the lock on the break-glass set past the lock timeout. | 409 with code break-glass.busy. No change is written. A retry is safe. | `GET /api/dustpan/v1/break-glass-accounts` |
| A guarded change comes without a fresh authentication. | 403 with code mfa.fresh-step-required. The text names the page that enrolls a factor. | `GET /api/dustpan/v1/break-glass-accounts` |

Evidence for the claims above:

- ADR 70 decisions 23 and 24 require two accounts and a guard that refuses a change that leaves fewer than two working paths. Proof: Pack ADR 70 decisions 23 and 24, and issue 623.
- ADR 306 decisions 3 and 6 stop disable, delete and sweep at the last enabled account. Proof: Pack ADR 306 decisions 3 and 6.
- The IP allowlist code is in internal/api/server/ip_allowlist.go and pkg/ipallow. Proof: The pack path list shows both as existing.
- The principal domain is in pkg/principal and the guides are in docs/guides. Proof: The pack path list shows both as existing.
- The passkey or hardware key requirement comes from decision 23 of ADR 70, and ADR 320 decision 23 repeats it. Proof: Pack issue 627 and ADR 320 decision 23 text: it signs in with a passkey or a hardware key only.

## What Changes

- A count of working break-glass accounts in pkg/principal with the four properties of ADR 70 decision 24: enabled, not expired, not blocked by a policy, not dependent on the identity provider. A second test, from decision 23, requires a passkey or a hardware key.
- A guard in the write transaction of each covered change. Disable, delete and sweep refuse when no enabled account would stay. Every other covered change refuses when it lowers the count below two.
- An atomic rotation route for a break-glass credential.
- A first-setup gate on two break-glass accounts with a passkey or a hardware key, with an upgrade report and an add path.
- A guide section for break-glass accounts only.

Out of scope:

- Typed reason, alert and session limit of break-glass sign-in (change break-glass-sign-in-typed).
- Reach recheck and act-as edges, and their guide text (change check-reach-when-grant-membership).
- Group trust switches and protected groups, and their guide text (change let-group-trust-identity-provider).
- The setting for the restore of access tables (change add-setting-lets-server-restore).
- Mode 2 recovery route (change break-glass-mode-2).

Depends on:

- add-setting-lets-server-restore
- `add-setting-lets-server-restore` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused change still costs one round trip. The 409 text names the live count and the route that lists the accounts.
- Setup with one account stops at finish. The setup status names the missing account and the passkey enrollment step.
- A person with no factor gets a refusal that names the enrollment page.

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
- Effect: Decision 24 gives the four properties of a working account. Decision 23 gives the passkey or hardware key rule. Every covered change except disable, delete and sweep keeps the floor of two. First setup needs two accounts.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Delete and sweep (owner, 2026-10-20, accepted as written for now). Delete and sweep follow the same "not the last one" rule. This changes ADR 70 decisions 25 and 26. The sweep may disable or delete a break-glass principal."
- Effect: Disable, delete and sweep use the rule that one enabled account must stay, with 409 in the write transaction. The floor of two is still checked after such a change and is reported in the answer as a warning with the live count. It is not a refusal. Each sweep action writes an audit record that names the sweep, the account and the reason.
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
- Effect: Rotation and the guarded changes of break-glass accounts use the one fresh-authentication window.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): proposal.md and spec guard-last-enabled: the owner decision P19 (Item 17) says every change other than disable, delete and sweep keeps the two-working-paths floor (ADR 70 d24), and first setup needs two. The spec and the proposal's error table apply only the last-enabled rule to all covered changes (revoke, IP allowlist, restore), so the two-path floor for those changes is dropped. Resolution: answered: The spec now has guard-last-enabled for disable, delete and sweep, and guard-floor for every other change, as the owner decision says.
- R1.2 (adr conflict): ADR 70 decision 24 (not owner-tagged, current text) says the server refuses each change that leaves fewer than two working paths, a restore included. The spec guard-last-enabled and the proposal's ADR check cite only ADR 306 and do not state that ADR 70 d24 floor is kept for revoke, credential revoke, IP allowlist and restore changes. Resolution: answered: The ADR 70 check now states that decision 24 keeps the floor for the other changes.
- R1.3 (missing scenario): No requirement or scenario covers a restore that leaves no enabled break-glass principal beyond a single line in the proposal table; the spec has no scenario for the restore refusal (break-glass.restore-refused), the staged-data check, or the restore order relative to the swap that design.md Risks requires. Resolution: answered: Requirement restore-guard and its scenario cover the restore, and a task tests it.
- R1.4 (missing scenario): Design Layers table requires a hostile test that a stale fresh step gets 403, but the spec has no scenario for fresh-authentication failure on disable, rotation or setup, and no scenario for the rotation route's authorization action. Resolution: answered: Requirement fresh-step covers the stale step on rotation. The rotation route has its own action.
- R1.5 (failure mode): Concurrent-write guard: design relies on row locks on the break-glass set, but no requirement covers the case where a second request for the last principal comes after the first commits, or the lock-timeout / deadlock path returns an error instead of 409. The failure answer for a lock conflict is not defined. Resolution: answered: Requirement guard-race defines the busy answer and the second request.
- R1.6 (open question): Setup gate reads a stored setup record listing two principal IDs (design decision 3), but the spec and the proposal say the count comes from enrolled passkeys. Which source is authoritative, and what happens when a listed account is later disabled or its passkey removed after setup finishes, is not stated. Resolution: answered: The live count is the one source. No stored list exists. The guard refuses a passkey removal below the floor.
- R1.7 (failure mode): Rotation: the spec says the new credential signs in before the old stops, but no scenario covers a failed insert of the new credential (duplicate, store error) and confirms the old credential stays active. Resolution: answered: A scenario covers the failed insert.
- R1.8 (wrong claim): The proposal says ADR 70 decision 23 and 24 require the guard counting only accounts with a passkey or hardware key; ADR 70 d24 as quoted counts an account with four properties (enabled, not expired, not policy-blocked, not IdP-dependent). The spec and tasks count only enabled accounts, dropping the expiry and policy properties. Resolution: answered: The count uses the four properties of ADR 70 decision 24 plus the passkey.
- R1.9 (open question): Delete of a break-glass principal by the sweep writes an audit record (ADR 306 d6), but the spec has no scenario for the sweep path, the audit record content (sweep, account, reason), or the notification to other admins required by ADR 306 d3. Resolution: answered: A sweep scenario covers the audit record. Notification of the other admins is left to the disable path of the existing change.
- R1.10 (scope): tasks.md 2.5 requires the security model guide to cover protected groups, trust, suspended and pinned memberships and reach; these belong to change check-reach-when-grant-membership and let-group-trust-identity-provider as listed in Out of scope, so the task misses its own item's scope while adding other items' work. Resolution: answered: The guide task now covers reach and break-glass only. Protected groups and trust stay out of scope.
- R1.11 (failure mode): Restore of an old backup that drops the second account is handled only in design risks; no task or scenario tests the restore path or its refusal, so a restore could still bypass the guard unnoticed. Resolution: answered: A failing test and a requirement cover the restore path.
- R2.1 (failure mode): The design says the count includes the passkey property and that a passkey removal is guarded, but no spec requirement or scenario covers passkey removal or credential change below the floor; guard-floor lists only revoke, credential change, expiry change and IP allowlist, so a passkey removal path is unguarded. File: openspec/changes/keep-two-working-break-glass/specs/break-glass-accounts/spec.md. Resolution: answered: guard-floor now lists a passkey removal, with a scenario, and a task tests it.
- R2.2 (owner decision conflict): Owner decision P19 is marked Provisional and the change applies it as binding; the change also does not state that the provisional answer was reviewed. File: proposal.md, ADR check section. Resolution: answered: The considered section states that the provisional answer was reviewed and is built as the reading.
- R2.3 (adr conflict): ADR 70 decision 24 says the server refuses each change that leaves fewer than two working paths, a restore included, but the guard-last-enabled spec and the sweep scenario apply a different rule; the change does not state how a disable that leaves exactly one working path (above zero enabled) is treated. File: specs/break-glass-accounts/spec.md. Resolution: answered: The ADR check and a scenario state that a disable that leaves one enabled account passes under ADR 306.
- R2.4 (open question): The design (Decision 2) says a change that does not lower the count is never refused, but guard-floor's scenario does not define the count rule for a change that lowers the count while one path exists; the two-rule split by change kind is not defined for a change kind that is both a disable and a revoke. File: design.md. Resolution: answered: Design decision 2 defines the rule for a change kind with both effects.
- R2.5 (missing scenario): No scenario covers a rotation or credential change that removes the last working credential while the account is still enabled, nor a restore where the staged count is at least two but the live state differs; design Decision 4 requires tests for both an old backup and a two-account backup, and the spec has only the one-account scenario. Resolution: answered: Scenarios now cover removal of the last credential and a restore with two accounts.
- R2.6 (failure mode): Design Decision 3 and the busy answer rely on a row lock on break-glass rows, but no scenario covers a deadlock between the guard and a concurrent restore or setup finish, nor the behavior when the lock is held by another transaction after the timeout returns 409 while a retry is safe. Resolution: answered: Design decision 3 sets one lock order, and a scenario covers restore in parallel.
- R2.7 (open question): The setup gate counts accounts with a passkey, but ADR 320 decision 23 allows a hardware key too; the spec does not say whether a hardware-key-only account counts toward the setup floor. Resolution: answered: A hardware key counts. The spec and a scenario say so.
- R2.8 (wrong claim): The ADR check cites ADR 216 decision 13 for the hosted-service break-glass rule (principals of the namespace with the role admin), while the proposal's What Changes and the setup gate speak of the installation; the change does not state which scope the count uses on the hosted service. Resolution: answered: The ADR 216 effect states the scope: namespace on the hosted service, installation when self-hosted.
- R2.9 (scope): Task 2.5 asks the guide to cover reach and break-glass, but the change's own item (#633) also covers protected groups; the change moves that scope out without a cross-reference to the item that owns it. Resolution: answered: Out of scope now names the change that owns the protected-group text.
- R2.10 (missing scenario): No scenario covers the grant revoke path for the sweep or the IP allowlist change with a concurrent restore; the sweep audit record scenario is listed in the adversarial pass but missing from the spec file. Resolution: answered: The sweep audit scenario is in the spec, and the restore and guarded change in parallel scenario is added.
- R3.1 (owner decision conflict): proposal.md and specs/break-glass-accounts/spec.md: the 'Changed by' and ADR 70 d23 checks cite ADR 320 d23 as a hardware-key source, but guard-floor and fresh-step give no requirement that a passkey removal below the floor is blocked by the stricter floor in the same write as an expiry change; the change applies P19 only partly by listing expiry and IP allowlist changes under the floor without a scenario for a policy-block change. Resolution: answered: guard-floor now lists policy block and expiry change, each with a scenario, and a passkey removal scenario exists.
- R3.2 (wrong claim): proposal.md ADR check cites ADR 70 decision 24 as a count of accounts with a passkey; the quoted ADR 70 d24 text does not state a passkey property, and ADR 320 d23 only states the sign-in method. The change attributes the passkey property to ADR 70 d24 without support. Resolution: answered: The ADR check now takes the passkey property from decision 23 and ADR 320 decision 23, not from decision 24.
- R3.3 (adr conflict): design.md Decision 2 and ADR 70 d24 quote 'refuses each change that leaves fewer than two working paths' while ADR 306 d3 refuses only a change that leaves no enabled account; design makes a disable that leaves one enabled account pass, but the working-path count (four properties) can be below two after that disable. The design does not check the working count for disable, so ADR 70 d24 is not applied to disable. Resolution: answered: ADR 306 decision 3 changes decision 24 for disable. The disable passes and the answer names the live count. This is stated in the ADR check and considered.
- R3.4 (adr conflict): ADR 227 decision 2 (owner) requires one fresh-authentication window for the guarded changes, but fresh-step in the spec returns 403 with mfa.fresh-step-required while ADR 301 d4 says a person with no factor is refused; the spec does not say how a person without a factor completes rotation or setup, which leaves a locked break-glass path unaddressed. Resolution: answered: A scenario and the considered section cover a person with no factor: 403 with an enrollment hint.
- R3.5 (missing scenario): No scenario covers restore on the hosted service, where ADR 216 d13 places the count in the namespace, nor a restore of a backup whose break-glass principals are in another namespace. Resolution: answered: The restore-guard requirement now has a hosted namespace scenario.
- R3.6 (failure mode): Two concurrent setup finishes and one concurrent passkey removal: the lock order is fixed in design but no scenario covers a crash after the first write and before commit, nor a retry after break-glass.busy on the setup finish path. Resolution: answered: Scenarios cover the setup retry after busy and two parallel setup finishes. A crash rolls back the transaction.
- R3.7 (open question): The count is not defined for an account whose policy block is set by a sweep or a group change in the same transaction; the four properties of ADR 70 d24 are checked but the order of checks is not stated. Resolution: answered: The count reads final rows in a fixed order of checks, stated in design and considered.
- R3.8 (scope): tasks.md 2.5 and the proposal disagree on the guide content: tasks cover reach and break-glass, while the proposal's What Changes lists only break-glass; reach text belongs to check-reach-when-grant-membership. Resolution: answered: The proposal and the tasks now cover the break-glass guide only. Reach text is out of scope.
- R3.9 (failure mode): A break-glass sign-in with a hardware key during a lock timeout is not covered; the busy answer may leave a sign-in session half-written. Resolution: answered: A sign-in takes no guard lock. A scenario covers a sign-in during a lock wait.

## Open questions

none

Questions considered and answered:

- Which threshold applies to which change? Owner decision, 13 Nov 2026 (queue P19), provisional and built as the reading: disable, delete and sweep stop at the last enabled account (ADR 306 d3, d6). Every other change keeps the floor of two working paths (ADR 70 d24). First setup needs two.
- Does a disable that leaves one enabled account break ADR 70 decision 24? No. ADR 306 decision 3 changes decision 24 for disable, delete and sweep. The change passes and the answer carries the live count so the administrator sees that the floor is below two. A later floor change from another kind is then refused only if it lowers the count.
- What counts as a working account, and where does the passkey property come from? ADR 70 decision 24 gives enabled, not expired, not blocked by a policy, not dependent on the identity provider. ADR 70 decision 23 and ADR 320 decision 23 give the passkey or hardware key. The count function applies both.
- In which order does the count check the properties, and how does it treat a policy block set in the same transaction? The count runs after the change is applied in the same transaction, on the final rows. It checks enabled, then expired, then policy block, then identity provider dependence, then passkey. A policy block that a sweep or a group change sets in the transaction is seen, because the count reads the final rows. A policy-block change uses the floor.
- What does a person with no enrolled factor do? ADR 301 decision 4 and ADR 227 decision 2 apply: the answer is 403 with mfa.fresh-step-required, and the text names the page that enrolls a passkey or TOTP. A break-glass account holds a passkey by decision 23, so it has a factor. Setup finish uses the one-time code and fresh step of the first administrator.
- Which scope does the count use, and what about restore on the hosted service? ADR 216 decision 13: the namespace on the hosted service, the installation when self-hosted. A restore counts the accounts of each restored namespace in the staged data, and a namespace backup counts its own namespace only. Break-glass principals in another namespace do not count.
- What happens after setup if a counted account loses its passkey? A passkey removal is a covered change that uses the floor, so it is refused when it lowers the count below two.
- Does the guide cover protected groups and reach? No. The changes let-group-trust-identity-provider and check-reach-when-grant-membership own that text. Owner decision, 13 Nov 2026 (queue P21) is for the first of them. This guide covers break-glass only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
