---
version: 1
---

Issue: #2547, #2996.

## Problem

A site administrator sets up LDAP sign-in and needs the sign-in to stay bounded and safe. The rebind runs before the 30 s group deadline starts, so the worst case of the group phase is about 40 s. The errors of checkEnabled and the bind gate wrap the errors of their callers, so the route must map each one to a fixed reason. No test runs the sign-in through the built binary.

## Evidence

- An in-process LDAPv3 test directory exists. Proof: internal/ldaptest/ldaptest.go:1 (package comment of ldaptest)
- The binary reads the bind password from a file variable. Proof: cmd/opendustpan/ldap.go:24 (ldapBindFileEnvVar)
- The error codes sign-in.failed and sign-in.unavailable exist. Proof: internal/errcode/catalog_signin.go:8 and internal/errcode/catalog_signin.go:11
- The provider password route exists. Proof: pack main: route POST /api/dustpan/v1/sign-in/providers/{}/password; coverage_gaps: signInWithProviderPassword
- The bind gate port exists. Proof: pkg/session/method.go:299 (type BindGate)

## Fix boundary

- Start the group deadline before the service-account rebind. The rebind gets its own limit of 10 s inside that deadline. The search gets the time that is left of the 30 s. A test covers the worst case.
- Map each error of checkEnabled and the bind gate to a fixed reason at the provider password route. A route test checks that no error text reaches the answer.
- Add a binary e2e test through ldaptest with --ldap-allowed-ranges, the password file, a check that the password variable is cleared, and good and wrong password sign-ins.

Out of scope:

- The flaky TestSlackSink/postgres test: the PM files its own issue for it.
- The rule that the LDAP subject is entryUUID or objectGUID only: the change subject-sources-can-change-cas keeps it.
- New LDAP settings, TLS changes, or changes to SAML, CAS, OAuth or trusted-header sign-in.
- Changes to group mapping rules.

## Check

- Write a method test in the NEW file internal/signinsvc/ldap_deadline_test.go that stalls the rebind for 11 s and expects the refusal inside the 30 s deadline. Check: TestLDAPGroupDeadlineCoversRebind fails on main: the refusal comes after the deadline plus the rebind time
- Write a route test in the NEW file internal/api/server/signin_password_reason_test.go that forces checkEnabled and bind gate errors with text and checks the fixed answers. It calls the public route POST /api/dustpan/v1/sign-in/providers/{provider}/password with no credential. Check: TestProviderPasswordRefusalsAreFixed fails on main: the answer holds the error text
- Write the binary e2e in the NEW file e2e/ldap_sign_in_binary_test.go. It calls the public route POST /api/dustpan/v1/sign-in/providers/{provider}/password with no credential. Check: TestLDAPSignInBinary fails on main: no test starts the binary with the LDAP flags
- Start the group deadline before the rebind in internal/signinsvc/password.go and give the rebind 10 s. Check: go test ./internal/signinsvc/... passes
- Map the errors to fixed reasons in the provider password handler under internal/api/server. Check: go test ./internal/api/server/... passes
- Run the binary e2e step against the real binary and ldaptest. It calls the existing public route POST /api/dustpan/v1/sign-in/providers/{provider}/password, which has no gate and needs no credential. Check: make e2e. Confirmed by: CI (make e2e)
- Update the LDAP sign-in docs page with the flags, the password file, the allowed ranges and the refusal text. Check: make docs-lint. Confirmed by: CI (make docs-lint)
- The site administrator completes the LDAP path from the docs page alone, and each error names the one command that helps. Check: A person checks the path. A person's step.

Depends on:

- subject-sources-can-change-cas
- `subject-sources-can-change-cas` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- An in-process LDAPv3 directory exists for tests. (`internal/ldaptest/ldaptest.go:7 (package ldaptest)`)
- The binary names the bind password file variable. (`cmd/opendustpan/ldap.go:24 (const ldapBindFileEnvVar)`)
- The bind gate port exists. (`pkg/session/method.go:299 (type BindGate)`)
- The sign-in error codes exist. (`internal/errcode/catalog_signin.go:8 (sign-in.failed)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P20)**, Item 18: #2915 subject sources that can change: "(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only). Reason: Entra, Okta and Kubernetes guidance; no users, so it costs nothing now and can be loosened later. ADR 321 (owner-accepted) text waits for the owner; ADR 70 d22 (owner) is kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (not owner-tagged).

- Decisions that apply: 9 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "Limits (PM). 10 seconds for a connection, 10 seconds for an operation, at most 1000 entries for a search of groups. A size limit of the directory answer refuses the sign-in. It is never a partial result."
- Effect: The rebind gets 10 s as one operation. The group phase starts its deadline earlier.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Refused by default (owner). The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Quote: "An explicit opt-in (owner). An installation can allow plain connections with one setting. The setting is off by default. (owner)"
- Effect: The e2e uses an explicit allowed range for a private address. The refusal stays without it.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What limit does the rebind get? ADR 321 decision 9 sets 10 s for an operation, so the rebind gets 10 s inside the group deadline of 30 s.
- Which subject attribute does the e2e use? entryUUID, supplied by the ldaptest directory. Owner decision, 13 Nov 2026 (queue P20) allows only entryUUID or objectGUID. The rule belongs to subject-sources-can-change-cas.
- Where does the flaky TestSlackSink/postgres go? It is another area. The PM files a separate issue.
- What if CI has no private non-loopback address? The e2e fails with a message that names the missing address. It never skips.
- Does a disabled user get a distinct answer? No. It gets the same 401 text as a wrong password. The audit entry records the class of the cause.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
