---
version: 2
---

Issue: #1325.

## Problem

A window of 0 turns the fresh check off, and the stale error omits the sign-in path.

## Evidence

- A requireFresh check exists in the server freshness handler. Proof: internal/api/server/freshness.go:82:func requireFresh(r *http.Request) error {
- A requireFresh check exists in the access token service. Proof: pkg/accesstoken/service.go:805 (func Service.requireFresh)
- Issue 1325 reports a zero window that turns the check off and a stale 403 without the sign-in path. Proof: Issue #1325 body: When the fresh window is 0, `requireFresh` returns nil, so the check is off.
- A fresh() function exists in breakglass. Proof: pkg/breakglass/breakglass.go:339 (func fresh)

## Fix boundary

- A window of 0 asks for fresh authentication at each guarded step, including age 0.
- The stale-session 403 names the sign-in path with code mfa.fresh-step-required.
- The fresh() function has a comment that names its error code.
- No-factor, token, narrower window, and boundary behavior match the quoted rules.

Out of scope:

- Decrypt route checks (owner decision pending)
- SCIM
- mTLS
- Collections and placement

## Check

- Add failing test for window 0 with old step refused Check: TestFreshWindowZeroAsksEachTime fails on main
- Add failing test for window 0 with age 0 refused Check: TestFreshWindowZeroAgeZeroRefused fails on main
- Add failing test for namespace 0 against installation 900 Check: TestFreshNarrowZeroAgainst900 fails on main
- Add failing test for 403 code and sign-in path Check: TestStaleErrorNamesSignInPath fails on main
- Add failing test for no-factor enrollment page Check: TestFreshNoFactorNamesEnrollment fails on main
- Add failing test for access-token refusal Check: TestFreshTokenNeverSatisfies fails on main
- Add failing test for age equal to nonzero window accepted Check: TestFreshBoundaryEqualsWindow fails on main
- Add failing test for window 30 and 1000 refused Check: TestFreshInvalidWindowRefused fails on main

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- requireFresh runs the freshness check in the server API (`internal/api/server/freshness.go:82`)
- Service.requireFresh checks fresh authentication for access tokens (`pkg/accesstoken/service.go:805`)
- fresh helper exists in breakglass (`pkg/breakglass/breakglass.go:339`)
- requireFreshStep checks fresh authentication for settings writes (`internal/api/server/settings.go:444`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2, 3).

- Decisions that apply: 1 (extended), 2 (current), 3 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A fresh authentication. A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71). It is fresh when its age is not above the window."
- Quote: "0 asks every time (owner, 2026-10-18). A window of 0 asks for a fresh authentication at each step. No value turns the check off. The other values are 60 to 900 seconds. A window of 0 is the strictest value, so a change to 0 needs no fresh authentication. A change to a longer window needs one, as ADR 212 decision 3 says for a weaker setting."
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3). The error names the path of a new sign-in. An access token never gives a fresh authentication."
- Effect: The change enforces 0 as ask-each-time, keeps age not above window as fresh for nonzero windows, refuses values outside 0 and 60 to 900, asks fresh for a change to a longer window and no fresh for a change to 0, and returns 403 mfa.fresh-step-required with the sign-in path.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No factor, no fresh step (owner). A person of these methods who has enrolled no factor cannot do a step that needs a fresh authentication. The refusal is `403` with the error code `mfa.fresh-step-required` (ADR 227 decision 6)."
- Effect: The no-factor refusal names the enrollment page with 403 mfa.fresh-step-required.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 18 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "The CLI and API clients. They keep their access tokens (ADR 53), with no change. An access token never gives a fresh authentication (ADR 227 decision 6)."
- Effect: An access token never satisfies a guarded step.
- Conflict: none.

## Open questions

1. Do the Pulumi CLI decrypt routes need a fresh authentication? Decides: owner.
   - Options: Add fresh check to Pulumi CLI decrypt routes; Leave decrypt routes without fresh check and record why.
   - Recommended: Leave decrypt routes without fresh check and record why Reason: ADR 227 decision 2 lists the steps that need fresh proof and does not name decrypt routes. The Pulumi CLI sends decrypt calls with an access token, which never gives fresh authentication.

Not spec-ready: a code check fails: threat 1 (A caller with a stale session or an acce): no guard and hostile test for encode or contain, limit; a code check fails: threat 2 (A person sets a longer narrow window to ): no guard and hostile test for encode or contain, authorize at point of use, fail closed.

Questions considered and answered:

- Does a window of 0 turn the fresh check off? No. ADR 227 decision 3 says 0 asks at each step and no value turns the check off.
- Must the stale-session refusal name the sign-in path? Yes. ADR 227 decision 6 requires 403 mfa.fresh-step-required and names the path of a new sign-in.
- Does a refused step need an audit entry? This change adds no new audit rule. The refusal changes nothing, as ADR 227 decision 6 says. Any audit rule for refusals is a separate change.
- Do other changes define their own fresh window or error code? No. This change owns the window rules and the code mfa.fresh-step-required; other changes reuse them.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
