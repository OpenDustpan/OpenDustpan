---
version: 1
---

Issue: #4274

## Why

**Persona: developer.** A developer does a write that needs a fresh authentication from the CLI with a step-up, or signs in with a TOTP factor that the developer can change or reset. When the developer changes or resets the TOTP factor, each step-up credential that the earlier factor made must stop working at once, in the same transaction as the change. Main has the port `stepup.CredentialStore` and no table and no adapter for it, so the TOTP store of the change `mfa-store` cannot revoke these credentials.

No person can reach this change yet. The person reaches it through the change `mfa` (through the TOTP store of the change `mfa-store`, which revokes the credentials of an earlier factor) and, later, through the step-up routes. This change has no user path of its own and no error that a person sees.

Evidence for the claims above:

- The port exists and has no adapter. Proof: `rg -n 'UseCredential|RevokeCredentialsOf' internal pkg --type go` gives only `internal/stepup/stepup.go:606`, `:609`, `:612` and `:616` (the port at `internal/stepup/stepup.go:593`).
- Main has no table for step-up credentials. Proof: `rg -n -i 'step_up_cred|stepup_cred' internal/store` gives no line.
- The TOTP store must revoke the credentials of the earlier factor. Proof: `internal/stepup/stepup.go:687` (`ConfirmTOTP`: "revokes each credential that the earlier factor made") and `internal/stepup/stepup.go:735` (`ResetTOTP`: "revokes each credential that TOTP made").
- No other change owns the table. Proof: `rg -l 'CredentialStore|UseCredential|RevokeCredentialsOf|step_up_credentials' openspec/changes --glob '!archive/**'` gives no change file before this change. The change `first-step-up-key-in` owns NEW `internal/store/sqlite/stepup.go` and NEW `internal/store/postgres/stepup.go` with the bootstrap marker, the lock of `AcceptStep`, the use of a key-add challenge, the admin challenge counter and the admin reset. It names no credential table and no `CredentialStore` method.

## What Changes

- Add the table `step_up_credentials` in the SQLite and PostgreSQL baselines, in place, with its queries.
- Add the SQLite and PostgreSQL adapters of `stepup.CredentialStore`: `UseCredential`, `RevokeCredentialsOfKey`, `RevokeCredentialsOfTOTP` and `RevokeCredentialsOfPerson`. Each revoke has a helper that runs in the transaction of its caller, so that the TOTP store, the key store and the reset store revoke in their own transaction.
- Add the NEW runner `stepuptest.RunCredentials` with the cases of `stepup.CredentialStore` that need no other step-up port, before any adapter implements the port.
- Add one backup and restore rule for the new table: a backup leaves it out and a restore leaves it empty.

Out of scope:

- The step-up challenge, the step-up key and the admin reset stores (`stepup.ChallengeStore`, `stepup.KeyStore`, `stepup.ResetStore`). A later change adds them, and `ChallengeStore.CompleteChallenge` then makes the credential rows with the insert query of this change.
- The TOTP factor store, owned by the change `mfa-store`
- Routes, the CLI, the console, the docs page and the e2e step

Depends on:

- none

## Usability

This change adds no route, no command and no setting, so the path of the person does not change. A restore ends each step-up credential, so a person does the step-up again after a restore. The step-up routes of a later change name that in their docs page.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has the port `stepup.CredentialStore` with `UseCredential` and the three revoke methods, and the store saves a credential only in `ChallengeStore.CompleteChallenge` (`internal/stepup/stepup.go:593`)
- Main has the credential `stepup.Credential` with no action, object, binding, namespace or actions (`internal/stepup/stepup.go:391`), and the use `stepup.Use` (`internal/stepup/stepup.go:443`). These bound values are fields of `stepup.Challenge` (`internal/stepup/stepup.go:231`)
- Main has the names of the cases of the credential store in `caseNames`, and `stepuptest.Run` skips each case and needs one store with each step-up port and `audit.Store` (`internal/stepup/stepuptest/suite.go:168`)
- Main has a runner of one port with its own harness, `mfatest.RunPasskeys` with `PasskeyCaseNames` (`pkg/mfa/mfatest/suite.go:117`)
- Main has the rule of an emptied table, `backup.EmptiedTables` with `passkey_challenges` (`pkg/backup/coverage.go:85`)

## ADR check

Each quote is copied from the file in `docs/adr/` or `docs/rfc/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged: decided by the project owner on 31 October 2026).

- Decisions that apply: 28.
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decisions 20 and 23; ADR 372 changes decision 17. None changes decision 28.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333, 372.
- Quote: "The TOTP factor stays in `internal/stepup` (`TOTPStore`), shared with the CLI step-up (RFC 21 O13). Each port has a conformance suite whose cases skip until the first adapter lands."
- Effect: the adapter of `stepup.CredentialStore` runs the conformance cases of the port, and the cases of the other step-up ports keep their skip.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decisions 1 and 2 are owner decisions).

- Decisions that apply: 1, 2.
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place. It adds no numbered migration."
- Quote: "Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: the table goes into `internal/store/sqlite/migrations/0168_baseline.sql` and `internal/store/postgres/migrations/0168_baseline.sql`, in place, with no migration code.
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged).

- Decisions that apply: 1, 5.
- Changed by: ADR 363 extends decision 2.
- Current rule: this ADR, with ADR 363.
- Quote: "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it."
- Quote: "I'm rejecting database native backups."
- Effect: the row references the person, the key, the TOTP factor and the challenge by their stable IDs. The backup rule is a rule of the portable backup (an emptied table).
- Conflict: none.

**RFC 102: A step-up of a person in the CLI** (status: discussing; an RFC, not a decision).

- Quote: "The step-up credential is a new credential type, so the owner accepts it in an ADR before work starts."
- Effect: this change adds storage only. It issues no credential, adds no route and gives no person a credential. The ADR that accepts the credential type stays required before a route issues a credential.
- Conflict: none with an ADR. See the open questions.

## Open questions

none

Questions considered and answered:

- Does the storage need the owner's ADR on the credential type first? Answered by the PM's direction of 2026-11-25 for this change: the storage lands with no route and no issue path, because the TOTP store of the change `mfa-store` must revoke in the same transaction. The rows stay empty until a route issues a credential, and that route waits for the ADR.
- How does a use check the action, the object, the binding and the window, when `stepup.Credential` has no such field? The row holds a copy of the bound values of the challenge, written in the transaction that makes the credential. A challenge does not change these values after its create. So `UseCredential` reads one row and needs no challenge table.
- How do the conformance cases make a credential, when the port has no save method? The harness of `stepuptest.RunCredentials` has a seed method. Each adapter test implements it with the insert query of this change, through an export file of its test package. The later `CompleteChallenge` calls the same query.
- Which credential cases stay in `stepuptest.Run`? "a completed credential keeps the hash and never the secret" needs `CompleteChallenge`, and "credentials of a revoked key stop working" needs `KeyStore.RevokeKey`. They keep their skip until those ports have adapters.
- Does a revoke delete the row? No. It makes the credential void, so a later use fails with `stepup.ErrUsed`, as the port says.
- Does the table need foreign keys to the key, the TOTP factor or the challenge? No. Those tables do not exist on main. A revoke is an explicit call, so the delete of a factor never removes a credential without the revoke. The person references `principals (id)`, as `passkeys` does.
- Does a restore keep step-up credentials? No. A credential lives minutes. A restore that brought one back could make a revoked credential work again. The table is an emptied table.

Drafted from the split of the change `mfa-store`.
