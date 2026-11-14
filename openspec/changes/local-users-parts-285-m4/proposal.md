---
version: 1
---

Issue: #2232, #2444, #2602.

## Problem

A site administrator manages local users and passkey sign-in. The last-credential rule fails closed even when a live passkey exists, a reset has no reach cover check, and the passkey start omits the documented allowCredentials list.

## Evidence

- RemovePassword fails closed in the store Proof: pkg/store/internal/localuserstore/localuserstore.go:557 (func RemovePassword)
- The passkey sign-in start exists Proof: internal/mfasvc/service.go:248 (func Service.BeginAssertion)
- allowCredentials is absent from non-test code Proof: rg -n -F --glob '!*_test.go' 'allowCredentials' pkg internal cmd: 0 matches
- The passkey guide exists Proof: docs/guides/passkeys.md
- The passkey reference exists Proof: docs/reference/passkeys-api.md

## Fix boundary

- RemovePassword and DeleteUser read the count of live passkeys in the same transaction; suspended passkeys do not count.
- A reset checks that the reach of the starter covers the reach of the person before it makes a code; the refusal uses the NEW error code `local-user.reset-denied`.
- The passkey start answer holds an explicit allowCredentials list.
- The guide and the reference match this shape.

Out of scope:

- MFA after a password, pending sign-ins and factor locks
- Email reset links
- Audit entry on an SSO change of signIn.localPassword.enabled
- password.hashMemoryBudgetKiB and demo person routes
- Passkey sign-in rate limits, 429 codes and Retry-After pending the sealed-cookie challenge change

## Check

- Add failing conformance tests for RemovePassword and DeleteUser in pkg/store/internal/localuserstore/localuserstore.go:557 (func RemovePassword) Check: go test ./pkg/store/... -run TestLastCredentialLivePasskeys fails on main
- Add a failing test for the reset reach refusal in internal/localusersvc Check: go test ./internal/localusersvc/... -run TestResetNeedsCoveringReach fails on main
- Add a failing test for the allowCredentials shape in internal/mfasvc/service.go:248 (func Service.BeginAssertion) Check: go test ./internal/api/server/... -run TestPasskeyOptionsShape fails on main
- Read the live passkey count in the removal transaction and fix stale comments Check: go test ./pkg/store/... passes
- Add the reach cover check and the NEW error code `local-user.reset-denied` Check: go test ./internal/localusersvc/... passes
- Return allowCredentials in the passkey start and update the passkey guide and reference Check: go test ./internal/api/server/... passes
- Verify wiring: POST /api/dustpan/v1/local-users/reset with a site-administrator access token, POST /api/dustpan/v1/sign-in/passkey/begin with no credential, and the docs pages Check: make e2e step and make check with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The demo seed exists (`internal/localusersvc/demo.go:111 (func NewDemoSeed)`)
- RemovePassword fails closed in the store (`pkg/store/internal/localuserstore/localuserstore.go:557 (func RemovePassword)`)
- The passkey sign-in start exists (`internal/mfasvc/service.go:248 (func Service.BeginAssertion)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged (all decisions)).

- Decisions that apply: 1 (extended), 4 (current), 17 (changed).
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decision 20; ADR 333 extends decision 23; ADR 372 changes decision 17. Chain to the end: ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333, 372.
- Quote: "A local user has a password, passkeys, or both (owner). A local user is one person principal with one local user record. The record holds the user name and the password hash. The passkeys of the person are records of the"
- Quote: "The last credential stays. The server refuses a change that leaves an enabled local user with no password and no passkey (`localuser.ErrLastCredential`). An administrator who wants to stop the sign-in of a person disable"
- Quote: "A reset code that an administrator makes (PM decision under the owner's delegation). An administrator starts a reset for a local user. The server makes a code of 32 random bytes, shows it once to the administrator, and s"
- Effect: Decision 4 requires the last-credential refusal. This change reads the live passkey count so the refusal matches the decision. Decision 17 requires an administrator-started reset. This change adds the reach cover check for the starter. It changes no words of an owner-tagged decision.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 22 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13; ADR 369 changes decision 9; ADR 372 changes. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 370, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354, 369, 372.
- Quote: "These rules close the side doors. - A change to an identifying attribute of a member of a protected group is a protected action. Examples are the user name, the email, the `externalId` and the federated subject. Account"
- Effect: The reset reach check follows the cover principle of this ADR. It changes no decision words.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Verify wiring: POST /api/dustpan/v1/local-users/reset with a" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Read the live passkey count in the removal transaction and f" names no file: name the file on main (path:line) or the NEW path that it writes; a code check fails: task "Add the reach cover check and the NEW error code `local-user" names no file: name the file on main (path:line) or the NEW path that it writes; a code check fails: and 1 more.

Questions considered and answered:

- Does the live passkey count or the reset reach check change owner-tagged words? No. ADR 285 decisions 4 and 17 already require the last-credential rule and the administrator-started reset. The change implements those words.
- Do passkey sign-in rate limits change here? No. The limits and the sealed-cookie challenge move are out of scope. The start shape returns an explicit allowCredentials list only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
