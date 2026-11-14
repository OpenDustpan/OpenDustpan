---
version: 1
---

Issue: #2285.

## Problem

A site administrator watches sign-in locks and needs to see a real lock. Today an attacker can send wrong passwords for many made-up names and cause one notice for about every 10 requests. The flood hides a real notice.

## Evidence

- The password sign-in route exists on main. Proof: ANY/POST /api/dustpan/v1/sign-in/password in the route list of the pack
- The policy route exists and its docs cell is weak. Proof: GET /api/dustpan/v1/local-users/policy, op getLocalUserPolicy, empty_cells docs weak in coverage_gaps
- The one-answer rule for password failures exists on main. Proof: error code sign-in.failed in internal/errcode/catalog_signin.go:8

## Fix boundary

- Limit local password name-lock notices to 10 for each installation and fixed UTC clock hour, then update one summary notice with the count left out and the top sources. Locks do not change.
- Hold the ceiling as installation setting localuser.lock-notice-ceiling with default 10 and allowed values 1 to 100, and show its effective value in GET /api/dustpan/v1/local-users/policy.

Out of scope:

- Change of lock thresholds, source budget, or password rules.
- Mail or webhook delivery of the notices.
- LDAP locks and passkey factor counts share no count with this ceiling.

## Check

- Write the failing ceiling test in file pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes): 30 made-up names in one fixed UTC hour give at most 10 notices and one updated summary with no user name. Check: `pkg/dustpan/localusers_test.go:203` NEW test TestLockNoticeCeilingSendsSummaryAfterTen fails on main
- Write the failing policy test in file pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes): the policy shows the ceiling and its effective value, and a set to 0 is denied. Check: `pkg/dustpan/localusers_test.go:203` NEW test TestLocalUserPolicyShowsNoticeCeiling fails on main
- Build the atomic hourly counter and summary update in one transaction on both dialects and wire the ceiling setting in file pkg/dustpan/localusers.go:206 (func newLocalUserService). Run the e2e flood step against POST /api/dustpan/v1/sign-in/password with no credential. Check: e2e flood step on sqlite and postgres against `POST /api/dustpan/v1/sign-in/password` with no credential, plus hour-boundary, failed-store, and parallel-instance cases pass
- Show the ceiling in GET /api/dustpan/v1/local-users/policy and change it through the installation settings resource with installation:manage-sign-in and an audit entry. Check: Policy and settings tests with an access token that holds installation:manage-sign-in pass
- Add the NEW file `docs/reference/local-users` for the local users docs page with the ceiling numbers and check the path from the page alone. Check: Docs page lists 10 notices, the summary contents, and the 1 to 100 range
- Run the full check in file pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes). Check: `make check` with zero findings, Confirmed by: CI (make check)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Local user error codes are built in the error catalog. (`internal/errcode/catalog_localuser.go:46 (func localUserSpecs)`)
- The local user service is wired in pkg/dustpan. (`pkg/dustpan/localusers.go:206 (func newLocalUserService)`)
- Local password routes have a test on main. (`pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged (all decisions)).

- Decisions that apply: 14 (current), 15 (current), 16 (changed).
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decision 20; ADR 333 extends decision 23; ADR 372 changes decision 17. Chain to the end: ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333, 372.
- Quote: "A lockout by name, shared by each instance (PM decision under the owner's delegation). The counts live in the database, behind the new port `authgate.NameLockCounter`, so every instance counts in one count. Three counts"
- Quote: "A limit by source as well (PM decision under the owner's delegation). The password sign-in has its own scope in the source budget of `authgate` (`password`), apart from the token lookups. A lockout by name alone lets one"
- Effect: Unknown names keep locking like real names. Only the notices get the ceiling that ADR 333 adds to decision 16.
- Conflict: none.

**ADR 333: Passkey challenges live in a sealed cookie, and name-lock alerts have a ceiling** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Lock alerts have a ceiling (owner, 2026-11-07). The alerts of ADR 285 decision 16 have a ceiling for each installation and hour. After the ceiling, the server writes one summary alert for the rest of the hour."
- Quote: "The ceiling is 10 alerts for each installation and hour. The summary alert gives the count of alerts left out and the top sources. The ceiling is a setting. The API shows its effective value."
- Effect: This change builds decision 5 as written and uses the audit kind of decision 4. It adds no new number and no new audit kind.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Should unknown names stop sending lock notices No. ADR 285 decision 14 locks unknown names like real names so a lock does not disclose which names exist. ADR 333 decision 5 keeps that rule and adds only a ceiling on notices.
- Which ceiling and summary apply ADR 333 decision 5: at most 10 notices for each installation and fixed UTC clock hour, then one summary notice for the rest of the hour with the count left out and the top sources.
- How is the ceiling set and shown Through the installation settings resource with action installation:manage-sign-in, default 10, allowed values 1 to 100, with an audit entry for each change, and the policy route shows the effective value.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
