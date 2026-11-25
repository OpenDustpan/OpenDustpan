---
version: 1
---

Issue: #4064

Roadmap: `docs/roadmap.md:149` (MFA).

## Why

**Persona: developer.** A developer shares an install with other people and CI pipelines. The developer signs in with a local or LDAP account and proves identity with a second factor. The sign-in needs a stored pending sign-in, stored recovery codes and a stored TOTP factor. Main has the ports for them and no adapter. This change is the first half of the change `mfa`: it adds the storage and nothing else, so that the gate, the route and the docs of the change `mfa` land on tested stores.

No person can reach this change yet. The person reaches it through the change `mfa` (requirement `require-mfa-password-signin`), which adds the gate, the route, the e2e step and the docs page. This change has no user path of its own and no error that a person sees.

Evidence for the claims above:

- The ports exist and have no adapter. Proof: pkg/mfa, internal/stepup, pkg/mfa/mfatest
- The change `mfa` needs the stores. Proof: openspec/changes/mfa/proposal.md

## What Changes

- Add the SQLite and PostgreSQL adapters of `mfa.PendingStore`, `mfa.RecoveryCodeStore` and `stepup.TOTPStore`, with their conformance suites first.
- Add one port method `AcceptSignInStep` that accepts a TOTP time step for a sign-in and touches no step-up counter.
- Add the four tables `mfa_pending_sign_ins`, `mfa_recovery_codes`, `mfa_totp_factors` and `mfa_totp_attempts`, with their queries.
- Add one backup and restore rule for each new table, and the carry step that a restore of an older backup needs so that it cannot end a lock or make a used recovery code valid again.

Out of scope:

- The gate, the route, the settings, the freshness checks, the e2e step and the docs page, owned by the change `mfa`
- TOTP enrollment, recovery code issue and the other factor routes, owned by app-level-second-factor-totp

Depends on:

- none

## Usability

This change adds no route, no command and no setting, so the path of the person does not change. The person reaches the stores through the change `mfa`.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has the pending sign-in in the file (read only) `pkg/mfa/mfa.go`: `mfa.Pending`, `mfa.PendingStore` with `CreatePending`, `FailPending` and `CompletePending`, and `mfa.RecoveryCodeStore`. This change adds no second pending type, and it reuses the single-use rule of the TOTP code through one new method.
- Main has no adapter of these ports or of `stepup.TOTPStore` in `internal/store`, and `mfatest.Run` skips each case (`pkg/mfa/mfatest/suite.go:110`)
- Main counts a TOTP failure on the step-up counter before the time step check: `BeginTOTPAttempt` comes before `AcceptStep` (`internal/stepup/stepup.go:705`)
- Main has the single-use rule of a TOTP code: the store accepts a time step one time, with `stepup.ErrUsed`, `TOTP.LastStep` and `AcceptStep` (`internal/stepup/stepup.go:728`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not owner-tagged).

- Decisions that apply: 1 (changed), 4.
- Changed by: ADR 55 extends decision 3; ADR 56 extends decision 6; ADR 57 extends; ADR 212 extends decision 8; ADR 285 changes decisions 1 and 2; ADR 301 changes decision 2. Chain to the end: ADR 55, ADR 56, ADR 57, ADR 212, ADR 285, ADR 301, ADR 70, ADR 297, ADR 320, ADR 321, ADR 69, ADR 71, ADR 222, ADR 227, ADR 245, ADR 232, ADR 235, ADR 365, ADR 312, ADR 333, ADR 142, ADR 210, ADR 216, ADR 306, ADR 332, ADR 354, ADR 369, ADR 286, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 149, ADR 247, ADR 273, ADR 370.
- Current rule: this ADR, with ADR 55, 56, 57, 212, 285, 301.
- Quote: "Built-in MFA. An install that signs people in with no SSO identity provider (local users, LDAP) can use built-in MFA."
- Quote: "Recovery codes. The server shows recovery codes one time, at enrollment."
- Quote: "The server stores factors and recovery codes per principal. This needs numbered migrations in each SQL dialect (CLAUDE.md)."
- Effect: the stores keep the digests of the recovery codes and the factors per person, with a numbered migration in each SQL dialect.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change add a route or a setting? No. The change `mfa` adds them.

Drafted from the split of the change `mfa`.
