---
version: 2
---

Issue: #624, #628.

## Why

**Persona: site administrator.** As a site administrator I keep last-resort access safe when the identity provider is lost, so I need break-glass sign-in to need a typed reason, raise an alert, end at a time limit that I can set, and remind me to test each account every 90 days.

The user path this change must make work:

1. **Docs page.** Site administrator opens the docs page for break-glass sign-in and starts a sign-in with a typed reason.
2. **API.** The passkey sign-in route receives the reason. The server checks the passkey assertion first. Only a valid assertion leads to the reason check, the alert audit entry in the sign-in transaction, a session with a time limit from the setting, and then a WARN log line.
3. **API.** Each request of the session writes an audit entry that names the session ID and the reason. Sign-out and the refresh route are exempt from the 503 rule.
4. **API.** The administrator reads the principal response from the principals routes in `internal/api/server/principals.go`. It shows the last test time and a flag for 90 days with no test.
5. **Console.** The console shows a banner for the flag with a link to the docs page.
6. **E2e.** BLOCKED by passkey sign-in for local users (issue 607). When it lands, the step signs in a break-glass account through `POST /api/dustpan/v1/sign-in/passkey/finish` with a passkey credential, a typed reason, checks the expiry and reads the flag.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Break-glass sign-in with no typed reason, or a reason that is too long or holds a control character. | 400: A break-glass sign-in needs a reason of 1 to 500 characters with no control character. | `POST /api/dustpan/v1/sign-in/passkey/finish` |
| Request with an expired break-glass session. | 401: The break-glass session reached its time limit. Sign in again with a reason. | `POST /api/dustpan/v1/sign-in/passkey/begin` |
| Break-glass sign-in started at the identity provider. | 403: A break-glass sign-in uses a passkey or a hardware key, not the identity provider. | `POST /api/dustpan/v1/sign-in/passkey/begin` |
| The audit write of a request of the session fails. | 503: The server could not record this request. Try again. | `POST /api/dustpan/v1/sign-in/passkey/begin` |

Evidence for the claims above:

- Issue 628 quotes ADR 70 decision 26 as saying the server reminds administrators to test each break-glass account every 90 days. The ADR text in the pack for decision 26 is cut off, so this change takes the 90-day rule from the issue text and does not claim more. Proof: docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md
- ADR 70 decision 26 says each break-glass sign-in needs a typed reason, raises an alert, and the audit log records each action of the session. The pack does not state the time limit value. Proof: docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md
- The principals API file exists and is where issue 628 asks for the new field. Proof: internal/api/server/principals.go
- The session package exists and is where issue 624 asks for the time limit. Proof: pkg/session
- The passkey sign-in routes exist in the API and are the sign-in path of a break-glass account. Proof: coverage gap list: POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish

## What Changes

- A break-glass sign-in without a typed reason is refused. The reason has 1 to 500 characters. Any Unicode control character, DEL and any line or paragraph separator is refused.
- The server checks the passkey assertion before it reads the reason. A failed assertion writes no alert entry and gives no session.
- A sign-in writes the alert audit entry and the break-glass.used notification in the sign-in transaction. If the audit write fails, the sign-in fails and no session exists. The server writes the WARN line after the commit, with CR and LF removed.
- The session limit is a key of the generic settings API, with the same range and shorter-of rule as the temporary administrator session limit. The session carries its expiry in the sealed access cookie. The server denies the request at or after the expiry, and refuses a cookie with an expiry later than the setting allows.
- The sealed access cookie also holds the reason. A tampered cookie fails to open and the request gets 401.
- Each request of the session writes an audit entry with the session ID and the reason. If that write fails, the request fails with 503 and does no work. Sign-out and the session end routes are exempt, so a person can always end a session.
- The last-test time is set in the same transaction as the sign-in. A refused sign-in is not a test. An account that never signed in shows the flag 90 days after creation.
- The principal response shows the last test time and a flag for 90 days with no test. These are new fields, so no client breaks.
- The change edits the baseline migration of each dialect in place and updates the table list. The store port gets a method to record the test, with a conformance test that both adapters run.
- The console shows a banner for the flag with a link to the docs page.
- A site administrator ends a session early with the session generation of the principal. The session check at each request reads the generation.
- A break-glass sign-in started at the identity provider is refused.

Out of scope:

- Two working break-glass paths and the disable guard (item keep-two-working-break-glass)
- The generic settings API itself (item add-one-generic-settings-api)
- Host recovery and temporary administrators
- The review workflow of a break-glass use beyond the audit entries that feed it
- The notification channel by email

Depends on:

- keep-two-working-break-glass
- add-one-generic-settings-api
- `keep-two-working-break-glass` (order of the milestone review)
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused reason says what to type and the limits.
- An expired session says the limit ended and that the person signs in again with a reason.
- The stale banner links to the docs page of the test sign-in, so the check costs one sign-in.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has the sign-in error code sign-in.failed. (`internal/errcode/catalog_signin.go:8 (sign-in.failed)`)
- Main has the sign-in error code sign-in.unavailable. (`internal/errcode/catalog_signin.go:11 (sign-in.unavailable)`)
- Main has a test that the inactivity sweep never disables break-glass. (`pkg/accesstoken/accesstoken_test.go:1270 (func TestSweepNeverDisablesBreakGlass)`)
- Main has the audit error code audit.reason-invalid. (`internal/errcode/catalog_audit.go:17 (audit.reason-invalid)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P19)**, Item 17: the break-glass floor: "the PM reading in build. Disable, delete and sweep stop at the last enabled account (ADR 306 d3); every other change keeps the two-working-paths floor (ADR 70 d24); first setup needs two. Reason: the stricter floor wherever the newer owner text is silent; owner ADRs 70 and 306 both kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 25 (changed), 26 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "Break-glass accounts are accounts of last resort."
- Quote: "The server records each use. Each break-glass sign-in needs a reason that the person types. It raises an alert."
- Effect: Follows the typed reason, the alert and the audit of each action. The session ends by denial at the limit, and the limit is a setting. This change adds no path to disable or delete an account.
- Conflict: none.

**ADR 142: A second-person review of each break-glass by the next business day** (not owner-tagged).

- Decisions that apply: 1 (changed), 3 (changed).
- Changed by: ADR 149 extends decisions 12 and 15; ADR 210 changes; ADR 222 changes decision 9; ADR 227 changes decision 9; ADR 297 changes decisions 1, 3 and 9. Chain to the end: ADR 149, ADR 210, ADR 222, ADR 227, ADR 297, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 301, ADR 320, ADR 339, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 149, 210, 222, 227, 297.
- Quote: "A review opens at the use."
- Effect: The review opens from the sign-in notification, written in the sign-in transaction. This change adds the session ID and the per-request audit entries that the review covers.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "each disable or delete by the sweep writes an audit record."
- Effect: No change to the words. This change adds no disable, delete or sweep path.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 20 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "Sign-in paths."
- Effect: The expiry lives in the sealed cookie. The passkey step keeps the pending sign-in cookie as it is today.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "IdP-started SAML is never a fresh sign-in (owner, 2026-11-07)."
- Effect: A sign-in at the identity provider never gives a break-glass session.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: threat 1 (An attacker replays or forges a passkey ): no guard and hostile test for encode or contain, limit, authorize at point of use; a code check fails: threat 2 (An attacker changes the sealed cookie to): no guard and hostile test for encode or contain, authorize at point of use, fail closed; a code check fails: threat 3 (A reason with CR, LF or a control charac): no guard and hostile test for boundary check, limit, authorize at point of use, fail closed; a code check fails: and 4 more.

Questions considered and answered:

- Does a break-glass sign-in need a typed reason, an alert and per-request audit? Yes. ADR 70 decision 26 requires them. ADR 142 decision 1 uses the session ID for the review.
- Does the last-test column need a numbered migration? No. The baseline migration of each dialect is edited in place until the first tag.
- Does this change keep the floor of two working paths? Yes. Owner decision, 13 Nov 2026 (queue P19): every change other than disable, delete and sweep keeps the floor. This change adds no way to disable or delete an account.
- Where does the session limit value come from? A setting in the generic settings API, with the range, the default and the shorter-of rule of the temporary administrator session limit. The effective value shows in the API.
- How does the session end at the limit? The sealed cookie holds the expiry. The server denies each request at or after it and writes an audit entry. Early end uses the session generation.
- Can the 503 rule trap a session? No. Sign-out and the early end of a session are exempt, so a person can always end a session.
- What if the alert audit write fails? The sign-in fails closed with no session, because the audit entry is the alert record.
- What if the process stops between the commit and the WARN line? The audit entry exists and is the alert of record. The WARN line can be lost.
- Can a wrong passkey assertion with a valid reason cause an alert? No. The assertion is checked first.
- Does the 90-day rule come from an ADR? The issue quotes ADR 70 decision 26 for it. The pack text is cut off, so this change cites the issue and states no more.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
