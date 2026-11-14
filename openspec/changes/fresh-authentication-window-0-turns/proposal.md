---
version: 4
---

Issue: #1325.

## Problem

A window of 0 turns the fresh check off, and the stale error omits the sign-in path. The installation rule says no value turns the check off.

## Evidence

- A requireFresh check exists in the server freshness handler. Proof: internal/api/server/freshness.go:82:func requireFresh(r *http.Request) error {
- Issue 1325 reports that a zero window turns the check off. Proof: Issue #1325 body: When the fresh window is 0, `requireFresh` returns nil, so the check is off.
- A fresh() function exists in breakglass. Proof: pkg/breakglass/breakglass.go:339 (func fresh)
- The settings write has its own fresh step. Proof: internal/api/server/settings.go:444

## Fix boundary

- requireFresh in internal/api/server/freshness.go asks each time for a window of 0, also at age 0.
- The stale 403 names the path of a new sign-in.
- The fresh() function has a comment that names mfa.fresh-step-required.

Out of scope:

- Decrypt route checks (owner decision pending)
- A stored namespace window setting
- SCIM
- mTLS
- Collections and placement

## Check

- Test window 0 refuses age 0 and an old step Check: TestFreshWindowZeroAsksEachTime fails on main
- Test the 403 names the sign-in path Check: TestStaleErrorNamesSignInPath fails on main
- Test age equal to a nonzero window is fresh Check: TestFreshBoundaryEqualsWindow fails on main
- Test an out-of-range window value is refused Check: TestFreshWindowConstructorRefusesOutOfRange fails on main
- Comment on fresh() names mfa.fresh-step-required Check: rg -n 'mfa.fresh-step-required' pkg/breakglass/breakglass.go
- Route keeps its authorization actions Check: Route test passes
- E2E step: POST /api/dustpan/v1/sign-in/providers/{provider}/password with a stale session cookie, then a guarded step, checks the 403 path Check: make e2e
- Docs page lists the 403 text Check: make docs-lint

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- requireFresh runs the freshness check in the server API (`internal/api/server/freshness.go:82`)
- requireFreshStep checks fresh authentication for settings writes (`internal/api/server/settings.go:444`)
- fresh helper in breakglass (`pkg/breakglass/breakglass.go:339`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 3).

- Decisions that apply: 3 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "0 asks every time (owner, 2026-10-18). A window of 0 asks for a fresh authentication at each step. No value turns the check off."
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3). The error names the path of a new sign-in."
- Effect: The change makes 0 ask each time and names the sign-in path in the 403.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 18 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "An access token never gives a fresh authentication (ADR 227 decision 6)."
- Effect: An access token never satisfies a guarded step.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Do the Pulumi CLI decrypt routes need a fresh authentication?
   - Options: Add a fresh check to the decrypt routes; Leave the decrypt routes unchanged and record why.
   - Provisional decision (owner review 14 Nov): Leave the decrypt routes unchanged and record why
   - Reason: The CLI sends an access token, which never gives fresh authentication, so a check would break each run. Benchmark: AWS: CLI calls with access keys do not ask to sign in again.
   - Persona impact: A check would break each CLI run with an access token. Escape hatch: The routes stay unchanged and the reveal step keeps its fresh check.
   - Owner-tagged ADRs involved: ADR 227, ADR 297 (their words do not change).

Questions considered and answered:

- Does a window of 0 turn the check off? No. ADR 227 decision 3 says no value turns the check off.
- Must the 403 name the sign-in path? Yes. ADR 227 decision 6 says the error names it.
- Is a refusal audited here? No new rule. ADR 227 decision 6 says a refused step changes nothing.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
