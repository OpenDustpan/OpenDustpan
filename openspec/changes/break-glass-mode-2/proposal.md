---
version: 3
---

Roadmap: `docs/roadmap.md:201` (Break-glass mode 2).

## Why

**Persona: operator.** Operator sets up an installation that must survive loss of every administrator credential. Operator needs an offline-key recovery path that gives one admin session, refuses forged or reused requests, and leaves a review record.

The user path this change must make work:

1. **Server start.** Set recovery mode to signed-request at first boot and record the offline public key.
2. **API.** Sign a recovery request offline with the operator key and submit it to the recovery route.
3. **Console.** Sign in with the issued admin session from the console and complete recovery.
4. **Docs page.** Follow the docs page to set the mode, make a request, submit it, and handle bad-signature and reuse errors.
5. **E2e.** Run the e2e step against the real binary over TLS: a valid request, a bad signature and a replay.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Recovery request has a wrong signature or wrong key | 401 with code break-glass.bad-signature and a message that names the failed check. | `opendustpan host recovery-status` |
| Second use of the same recovery request | 409 with code break-glass.reused-request and a message that names the reuse. | `opendustpan host recovery-request --help` |
| Recovery attempt on an installation not in signed-request mode | 403 with code break-glass.wrong-mode and a message that names the active mode. | `opendustpan host recovery-status` |

Evidence for the claims above:

- ADR 57 defines mode 2 as a recovery request signed with an offline operator key. Proof: docs/adr/0057-break-glass-recovery.md decision 1
- The milestone step requires one admin session on a signed request, refusal on wrong signature or reuse, and a break-glass review. Proof: docs/roadmap.md:201 step 11 text in the context pack
- The host recovery command already has an off-mode test, so the mode setting exists on main. Proof: TestHostRecoveryOff at cmd/opendustpan/host_test.go:750

## What Changes

- First-boot recovery mode supports signed-request with a pinned offline public key and one recovery administrator.
- The recovery route accepts an offline-signed request, refuses bad signatures and replays, and issues one session to the recovery administrator.
- Each use writes an audit entry with mode, time and source. The review kind and scope wait for the RFC.
- Success adds 1 to the session generation of the recovery administrator and ends its prior sessions.
- The session lifetime uses the one freshAuthSeconds window.

Out of scope:

- Mode 1 bootstrap token flow except refusal when the mode is signed-request
- Mode 3 restore from backup
- Break-glass account daily use

Depends on:

- add-one-generic-settings-api
- break-glass-sign-in-typed
- fresh-authentication-window-0-turns
- `add-one-generic-settings-api` (order of the milestone review)
- `break-glass-sign-in-typed` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Operator must keep the offline key safe with no in-product guidance past the docs page.
- A replay error needs a new offline signature, which costs a trip to the offline key.
- A second live recovery ends the first session; the docs page says so.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Sweep never disables the last break-glass principal. (`pkg/accesstoken/accesstoken_test.go:1270`)
- The host recovery off path has a test. (`cmd/opendustpan/host_test.go:750`)
- Error codes sign-in.failed and sign-in.unavailable exist. (`internal/errcode/catalog_signin.go:8`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 57: Break-glass recovery when the last administrator loses every factor** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (changed), 5 (current), 6 (current).
- Changed by: ADR 70 changes decision 4; ADR 245 changes decision 2. Chain to the end: ADR 70, ADR 245, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 295, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 333.
- Current rule: this ADR, with ADR 70, 245.
- Quote: "a recovery request signed with an offline operator key (mode 2)"
- Quote: "The operator chooses the mode at first boot."
- Quote: "Every break-glass use writes an audit entry."
- Quote: "The break-glass setting and each recovery step have a REST API and an action (ADR 17)"
- Effect: Implements mode 2: first-boot choice, audit entry on each use, REST API with action.
- Conflict: none.

**ADR 142: A second-person review of each break-glass by the next business day** (not owner-tagged).

- Decisions that apply: 1 (changed), 2 (current), 3 (changed).
- Changed by: ADR 149 extends decisions 12 and 15; ADR 210 changes; ADR 222 changes decision 9; ADR 227 changes decision 9; ADR 297 changes decisions 1, 3 and 9. Chain to the end: ADR 149, ADR 210, ADR 222, ADR 227, ADR 297, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 301, ADR 320, ADR 339, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 149, 210, 222, 227, 297.
- Quote: "Two kinds of use."
- Quote: "The opt-in of the installation."
- Quote: "A review opens at the use."
- Effect: Decision 1 names only freeze-break and account-session. A recovery-session kind needs an RFC first. The review part of this change is blocked until the RFC is accepted.
- Conflict: rfc_needed.

**ADR 245: Host recovery: database access, recovery modes and break-glass controls** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: ADR 295 changes decision 12. Chain to the end: ADR 295.
- Current rule: this ADR, with ADR 295.
- Quote: "The command respects the recovery mode (product manager)."
- Quote: "The recovery mode setting."
- Effect: Follows the stored recovery-mode setting; none refuses recovery.
- Conflict: none.

**ADR 227: One fresh-authentication window** (not owner-tagged).

- Decisions that apply: 1 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A fresh authentication is a built-in MFA step (ADR 46)"
- Effect: The session lifetime uses the one freshAuthSeconds window.
- Conflict: none.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 55 extends decision 3; ADR 56 extends decision 6; ADR 57 extends; ADR 212 extends decision 8; ADR 285 changes decisions 1 and 2; ADR 301 changes decision 2. Chain to the end: ADR 55, ADR 56, ADR 57, ADR 212, ADR 285, ADR 301, ADR 70, ADR 297, ADR 320, ADR 321, ADR 69, ADR 71, ADR 222, ADR 227, ADR 245, ADR 232, ADR 235, ADR 365, ADR 312, ADR 333, ADR 142, ADR 210, ADR 216, ADR 306, ADR 332, ADR 354, ADR 286, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 149, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 55, 56, 57, 212, 285, 301.
- Quote: "What MFA covers. Console sign-in, the creation of access tokens, and the reveal of a secret value (ADR 41)."
- Effect: A recovery session with no factor at issue needs an RFC exemption. That part is blocked until the RFC is accepted.
- Conflict: rfc_needed.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 12 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "One session generation for each person."
- Effect: Success adds 1 to the session generation of the recovery administrator.
- Conflict: none.

## Open questions

1. Which review scope and use kind does mode 2 use, and does the review open when the opt-in is off? Decides: PM.
   - Options: Installation scope, new use kind recovery-session, review always opens; Installation scope, kind account-session, opt-in rule of ADR 142.
   - Recommended: Installation scope, new use kind recovery-session, review always opens Reason: Every recovery is high risk and needs a record. Needs an RFC first.
2. Does the recovery session need an MFA step at issue? Decides: PM.
   - Options: No step: no factor exists, the session is short and recovery-only; Require an enrolled factor.
   - Recommended: No step: no factor exists, the session is short and recovery-only Reason: No factor exists when every administrator is lost. Needs an RFC first.
3. What is the signed-request format? Decides: PM.
   - Options: Ed25519 signed JSON with kid, request ID, installation ID, audience, issued-at, expiry; X.509 signed request.
   - Recommended: Ed25519 signed JSON with kid, request ID, installation ID, audience, issued-at, expiry Reason: One simple scheme for offline signing.
4. How long is a used request ID kept? Decides: PM.
   - Options: Until expiry plus 7 days; Forever.
   - Recommended: Until expiry plus 7 days Reason: Bounded growth and replay refusal in the risk window.
5. What maximum request lifetime and skew? Decides: PM.
   - Options: 15 minutes and 120 seconds; 60 minutes and 300 seconds.
   - Recommended: 15 minutes and 120 seconds Reason: Short window with a small skew bound.
6. How does the pinned key rotate? Decides: PM.
   - Options: Host command with audit entry; Signed rotation request from the old key.
   - Recommended: Host command with audit entry Reason: Host access is a separate trust root.
7. May a second recovery session be live? Decides: PM.
   - Options: A second valid request ends the first session; Refuse until the first ends.
   - Recommended: A second valid request ends the first session Reason: An operator who lost the first session recovers at once. Both uses are audited.

Not spec-ready: the change conflicts with ADR 142: an RFC comes first; the change conflicts with ADR 46: an RFC comes first.

Questions considered and answered:

- When is mode 2 selected? ADR 57 decision 2 and ADR 245 decision 6: first boot sets signed-request and later boots keep it.
- Does a use write audit records? Yes, ADR 57 decision 5. The review part waits for the RFC on ADR 142 decision 1.
- Do the ADRs fix the signature scheme? No. It is an open question.
- Does MFA apply at issue? ADR 46 decision 6 covers console sign-in. No ADR exempts recovery, so an RFC is needed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
