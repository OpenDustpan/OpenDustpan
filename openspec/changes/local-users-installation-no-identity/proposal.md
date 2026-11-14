---
version: 1
---

Issue: #2276, #2278, #2483.

## Problem

A new production installation with only local users has no working path in the guides to the first grant. The documented call with the bootstrap admin token answers 403 mfa.fresh-step-required.

## Evidence

- console-sign-in.md tells the operator to give the first roles with the bootstrap admin token through the grants API. Proof: Issue #2276 and issue #2483 section 1a quote that sentence. docs/guides/console-sign-in.md exists on main (pack paths).
- POST /api/server/grants and POST /api/server/namespaces/acme/grants with the bootstrap token answer 403 mfa.fresh-step-required. Proof: Issue #2276 step 2 and issue #2483 section 1a. The error code exists at internal/errcode/catalog_mfa.go:15.
- host create-temp-admin gives a token, not a session. Proof: Issue #2276 step 3. RFC 117 lists ADR 245 decision 4: a temporary administrator uses only the printed token.
- Only opendustpan dev gives an administrator session today. Proof: RFC 117 facts: dev grants site-administrator to dev:admin at the one-time link sign-in; serve never serves that link.

## Fix boundary

- docs/guides/console-sign-in.md: replace the sentence that tells the operator to give the first roles with the bootstrap admin token. State the 403 mfa.fresh-step-required answer. Say that grant, role, group-member and gate writes need a console session with a recent sign-in.
- docs/guides/local-users.md: state that a local user with no grant, and a temporary administrator token, cannot make the first grant. Name opendustpan dev as the only path that gives an administrator session today. State that the production path waits for the owner decision in RFC 117.
- docs/guides/host-recovery.md: state that the temporary administrator token cannot make grants, because it never gives a fresh sign-in. Keep the expiry text as it is.

Out of scope:

- Any behavior change to fresh authentication, bootstrap token reach or temporary administrator sessions (RFC 117, owner decision).
- The curl examples of connect-github.md and the runner API answer for a temporary administrator.
- The expiry limit of a temporary administrator: owner decision P12 sets a setting, and a separate item builds it.
- Other doc differences of issues 2278 and 2483.

## Check

- Correct the first-sign-in section of console-sign-in.md. Check: `rg -n -F 'Give the first roles with the bootstrap admin token' docs/guides/console-sign-in.md` returns no match, and `rg -n 'mfa.fresh-step-required' docs/guides/console-sign-in.md` returns a match.
- Correct local-users.md and host-recovery.md. Check: `rg -n 'mfa.fresh-step-required' docs/guides/local-users.md docs/guides/host-recovery.md` returns a match in each file.
- Find other instructions that name a token for a grant, gate or group-member write. Check: `rg -n -i 'bootstrap admin token' docs/guides` lists no sentence that tells the operator to make a grant, gate or group change with a token.
- Run docs lint and the usability check: the operator reads the guides and meets no instruction that answers 403. Check: `make docs-lint` and `make check` pass with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The error code mfa.fresh-step-required exists. (`internal/errcode/catalog_mfa.go:15 (error code mfa.fresh-step-required)`)
- The password sign-in handler of local users exists. (`internal/api/server/localusers.go:336 (func Handler.passwordSignIn)`)
- The site administrator check by role exists. (`pkg/access/service.go:897 (func Service.isSiteAdministrator)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P12)**, Batch 10: #952 temporary administrator session limit: "1 hour default, as a setting; the record and its token end at the shorter of the setting and the token policy maximum. Reason: the owner's own words (21.6, and ADR 306 d1 "session limit 1 hour, as a setting"); short-lived emergency access is the norm (AWS, Microsoft). ADR 245 d10 (24 h) text waits for the owner."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 227: One fresh-authentication window** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends; ADR 373 extends decision 1. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354, 373.
- Quote: "An access token never gives a fresh authentication."
- Effect: The guides state this rule as it is. The change keeps the rule and edits no code.
- Conflict: none.

**ADR 245: Host recovery: database access, recovery modes and break-glass controls** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: ADR 295 changes decision 12; ADR 372 changes decision 3. Chain to the end: ADR 295.
- Current rule: this ADR, with ADR 295, 372.
- Quote: "It uses only the token that the command printed."
- Effect: The host-recovery guide says that the printed token gives no fresh sign-in, so it cannot make grants.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Run docs lint and the usability check: the operator reads th" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or .

Questions considered and answered:

- Does any accepted ADR let the bootstrap admin token pass the fresh-authentication step? No. ADR 227 decision 6 says an access token never gives a fresh authentication, and RFC 117 finds no exception.
- Can a temporary administrator make the first grant? No. The token is an access token (ADR 245 decision 4). Owner decision, 13 Nov 2026 (queue P12): the expiry becomes a setting of 1 hour by default. This change does not touch the expiry text.
- Does the owner question block this docs change? No. The guides state current behavior only. The first administrator path is owned by RFC 117 and a separate item.
- Do other sentences of the guides still tell the operator to use a token for grants, gates or group-member writes? A task runs rg over docs/guides for the phrase. Any match that names a token for such a write is fixed in this change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
