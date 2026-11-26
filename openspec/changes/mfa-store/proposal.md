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
- The port comments of `ConfirmTOTP` and `ResetTOTP` promise a revoke of the step-up credentials of the earlier factor, and no step-up credential type, table or issuer exists. Proof: `internal/stepup/stepup.go:687` (`ConfirmTOTP`), `internal/stepup/stepup.go:735` (`ResetTOTP`), and `rg -n -i 'step_up_cred' internal/store` gives no line.

## What Changes

- Change the port `mfa.PendingStore.CompletePending` to take one `mfa.Completion` value in place of the session, its digest and the audit entry (task 1.1), so that it meets the argument limit of 5. the NEW constructor `mfa.NewCompletion` rejects a zero session ID, a zero digest and an entry with no action.
- Add the SQLite and PostgreSQL adapters of `mfa.PendingStore`, `mfa.RecoveryCodeStore` and `stepup.TOTPStore`, with their conformance suites first.
- Add one port method `AcceptSignInStep` that accepts a TOTP time step for a sign-in and touches no step-up counter.
- Add the four tables `mfa_pending_sign_ins`, `mfa_recovery_codes`, `mfa_totp_factors` and `mfa_totp_attempts`, with their queries.
- Add one backup and restore rule for each new table, and the carry step that a restore of an older backup needs so that it cannot end a lock or make a used recovery code valid again.
- Implement each method of `stepup.TOTPStore`: `EnrollTOTP`, `ConfirmTOTP`, `TOTP`, `BeginTOTPAttempt`, `AcceptStep`, `ResetTOTP` and the new `AcceptSignInStep`. `ConfirmTOTP` and `ResetTOTP` delete the factor and append the entry; they revoke nothing, because no step-up credential exists. The port comments of these two methods change to say so (task 2.1).
- Add the NEW runner `stepuptest.RunTOTP` with the TOTPStore cases that need no step-up credential, two NEW cases of the confirm and the reset without the revoke, and the `AcceptSignInStep` cases. `stepuptest.Run` stays unwired until the adapters of `stepup.KeyStore`, `stepup.ChallengeStore` and `stepup.ResetStore` exist.

Out of scope:

- The gate, the route, the settings, the freshness checks, the e2e step and the docs page, owned by the change `mfa`
- TOTP enrollment, recovery code issue and the other factor routes, owned by app-level-second-factor-totp
- The table and the adapter of `stepup.CredentialStore`. A later change adds them, and that change implements the revoke in `ConfirmTOTP` and `ResetTOTP` in the same change that adds the table. No route issues a step-up credential before then.
- The adapters of `stepup.KeyStore`, `stepup.ChallengeStore` and `stepup.ResetStore`

Depends on:

- none

## Usability

This change adds no route, no command and no setting, so the path of the person does not change. The person reaches the stores through the change `mfa`.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has the pending sign-in in the file (read only) `pkg/mfa/mfa.go`: `mfa.Pending`, `mfa.PendingStore` with `CreatePending`, `FailPending` and `CompletePending`, and `mfa.RecoveryCodeStore`. `CompletePending` has six parameters (`pkg/mfa/mfa.go:315`), one more than the argument limit of the linter (5), and no Go file on main calls it. This change adds no second pending type, and it reuses the single-use rule of the TOTP code through one new method.
- Main has no adapter of these ports or of `stepup.TOTPStore` in `internal/store`, and `mfatest.Run` skips each case (`pkg/mfa/mfatest/suite.go:110`)
- Main counts a TOTP failure on the step-up counter before the time step check: `BeginTOTPAttempt` comes before `AcceptStep` (`internal/stepup/stepup.go:705`)
- Main has the single-use rule of a TOTP code: the store accepts a time step one time, with `stepup.ErrUsed`, `TOTP.LastStep` and `AcceptStep` (`internal/stepup/stepup.go:728`)
- Main has six methods in `stepup.TOTPStore` (`internal/stepup/stepup.go:680`): `EnrollTOTP`, `ConfirmTOTP`, `TOTP`, `BeginTOTPAttempt`, `AcceptStep` and `ResetTOTP`. `AcceptSignInStep` is the new seventh method of task 2.1.
- Main has the TOTPStore case names in `caseNames` of `internal/stepup/stepuptest/suite.go`, and `stepuptest.Run` skips each case and needs one store with each step-up port and `audit.Store` (`internal/stepup/stepuptest/suite.go:168`). No adapter of `KeyStore`, `ChallengeStore` or `ResetStore` exists.
- Main has no person fixture with a TOTP factor. A conformance case creates its persons with `principal.RecordStore.CreatePrincipal` first, because `mfa_totp_factors.person_id` references `principals (id)`.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not owner-tagged).

- Decisions that apply: 1 (changed), 4.
- Changed by: ADR 55 extends decision 3; ADR 56 extends decision 6; ADR 57 extends; ADR 212 extends decision 8; ADR 285 changes decisions 1 and 2; ADR 301 changes decision 2. Chain to the end: ADR 55, ADR 56, ADR 57, ADR 212, ADR 285, ADR 301, ADR 70, ADR 297, ADR 320, ADR 321, ADR 69, ADR 71, ADR 222, ADR 227, ADR 245, ADR 232, ADR 235, ADR 365, ADR 312, ADR 333, ADR 142, ADR 210, ADR 216, ADR 306, ADR 332, ADR 354, ADR 369, ADR 286, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 149, ADR 247, ADR 273, ADR 370.
- Current rule: this ADR, with ADR 55, 56, 57, 212, 285, 301.
- Quote: "Built-in MFA. An install that signs people in with no SSO identity provider (local users, LDAP) can use built-in MFA."
- Quote: "Recovery codes. The server shows recovery codes one time, at enrollment."
- Quote: "The server stores factors and recovery codes per principal. This needs numbered migrations in each SQL dialect (CLAUDE.md)."
- Effect: the stores keep the digests of the recovery codes and the factors per person. The schema form of decision 4 is replaced for the time before the first tag by ADR 322 (owner): the tables go into the baseline of each dialect, in place, with no numbered migration.
- Conflict: none.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged: decided by the project owner on 31 October 2026).

- Decisions that apply: 28.
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decisions 20 and 23; ADR 372 changes decision 17. None changes decision 28.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333, 372.
- Quote: "The TOTP factor stays in `internal/stepup` (`TOTPStore`), shared with the CLI step-up (RFC 21 O13). Each port has a conformance suite whose cases skip until the first adapter lands."
- Effect: the TOTP adapter implements `stepup.TOTPStore` in `internal/stepup` and runs its conformance cases; the cases of the step-up ports with no adapter keep their skip.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decisions 1 and 2 are owner decisions).

- Decisions that apply: 1, 2.
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place. It adds no numbered migration."
- Effect: the four tables go into `internal/store/sqlite/migrations/0168_baseline.sql` and `internal/store/postgres/migrations/0168_baseline.sql`, in place.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change add a route or a setting? No. The change `mfa` adds them.
- Do `ConfirmTOTP` and `ResetTOTP` revoke step-up credentials? Not in this change: no credential type, table or issuer exists, so there is nothing to revoke. The step-up credential needs the owner's ADR first (RFC 102). The change that adds the credential table implements the revoke in both methods in the same change, and moves the two revoke cases into `stepuptest.RunTOTP`. No route issues a step-up credential before then.
- Which conformance cases does the TOTP adapter run? The NEW runner `stepuptest.RunTOTP` runs 27 TOTPStore cases, two NEW cases of the confirm and the reset without the revoke, and the 5 `AcceptSignInStep` cases. The two cases that check a revoke of credentials stay in `stepuptest.Run` with their skip. `stepuptest.Run` needs a store with each step-up port, so it stays unwired until the adapters of `KeyStore`, `ChallengeStore` and `ResetStore` exist.

Drafted from the split of the change `mfa`.
