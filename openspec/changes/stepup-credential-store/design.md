## Context

The storage of the step-up credential: one table in each dialect, the SQLite and PostgreSQL adapters of `stepup.CredentialStore`, the conformance cases of the port and the backup rule of the table. The TOTP store of the change `mfa-store` revokes the credentials of an earlier factor through this store, in its own transaction. A later change adds the challenge store, whose `CompleteChallenge` makes the rows.

## Decisions

1. **One row holds all that a use checks.** `stepup.Use` carries the action, the object, the namespace, the binding and, for a window, the excluded state. `stepup.Credential` does not hold these values: `stepup.Challenge` does. The row of `step_up_credentials` holds the fields of `stepup.Credential` and a copy of the bound values of its challenge (`action`, `object`, `namespace`, `actions`, the `binding_*` columns). The insert query writes the copy in the transaction that makes the credential, so the copy is a derived fact written in the transaction of its source. A challenge does not change these values after its create. So `UseCredential` reads one row and needs no challenge table.
2. **The use is one conditional update.** `UseCredential` runs in one transaction: it reads the row by the secret hash, checks the expiry, the uses left and the void flag, checks each bound value, and takes one use with `UPDATE step_up_credentials SET uses_left = uses_left - 1 WHERE id = ? AND uses_left > 0 AND void = 0`. When the update changes no row, the call fails with `stepup.ErrUsed`. So of two calls at the same time on the last use, one wins, in SQLite and in PostgreSQL. The order of the checks is: `stepup.ErrNotFound`, `stepup.ErrExpired`, `stepup.ErrUsed`, `stepup.ErrMismatch`, `stepup.ErrFactorRefused`.
3. **A mismatch makes the credential void in the same transaction as its audit entry.** On `stepup.ErrMismatch` and `stepup.ErrFactorRefused` the adapter sets `void` and appends the failure entry of the use in one transaction. A failed append rolls back the void. On `stepup.ErrNotFound`, `stepup.ErrExpired` and `stepup.ErrUsed` it changes nothing and appends nothing.
4. **A revoke sets the void flag.** The three revoke methods set `void` on each row of the key, of the TOTP factor or of the person, and delete no row. Each method has an unexported helper that takes the queries of a transaction, so the TOTP store, the key store and the reset store call it in their own transaction. A revoke of no row is not an error.
5. **The stable IDs, and no foreign key to a table that main does not have.** The row references the person by `person_id` (references `principals (id)`, as `passkeys` does), and the key, the TOTP factor and the challenge by their IDs. Main has no table of keys, TOTP factors or challenges, so these columns have no foreign key. The delete of a factor revokes its credentials with an explicit call (decision 4), never through a cascade.
6. **The conformance cases come first, in their own runner.** `stepuptest.Run` needs one store with each step-up port. No adapter has the other ports, so the NEW runner `stepuptest.RunCredentials` runs the cases of `stepup.CredentialStore` with a harness of its own. The harness has a seed method, because the port has no save method: each adapter test implements it with the insert query of decision 1, through an export file of its test package. Two cases need other ports and stay in `stepuptest.Run`, with their skip: "a completed credential keeps the hash and never the secret" (`ChallengeStore.CompleteChallenge`) and "credentials of a revoked key stop working" (`KeyStore.RevokeKey`). The harness creates each person with `principal.RecordStore.CreatePrincipal` before the seed.
7. **A backup carries no credential, and a restore leaves the table empty.** The table goes in `EmptiedTables`, as `passkey_challenges` does. A credential lives for minutes. A restore that brought a row back could make a used or a revoked credential work again.

## Structure

- `internal/stepup/stepuptest/credentials.go` (NEW): `CredentialStore` (the harness: `stepup.CredentialStore`, `audit.Store`, `principal.RecordStore` and `SeedCredential(ctx, Seed) error`), `Seed` (`Credential stepup.Credential` and `Challenge stepup.Challenge`, the source of the bound values), `CredentialFactory`, `CredentialCaseNames` and `RunCredentials`.
- `internal/stepup/stepuptest/suite.go`: the 18 case names of decision 6 move from `caseNames` to the list of internal/stepup/stepuptest/credentials.go. Two stay.
- `internal/store/sqlite/stepup_credentials.go` and `internal/store/postgres/stepup_credentials.go` (NEW): the four methods and the unexported helpers of decision 4.
- `internal/store/sqlite/stepup_credentials_export_test.go` and `internal/store/postgres/stepup_credentials_export_test.go` (NEW, in the package of the store): `SeedStepUpCredential`, which calls the insert query.
- `internal/store/sqlite/stepup_credentials_test.go` and `internal/store/postgres/stepup_credentials_test.go` (NEW): the call of `stepuptest.RunCredentials` and of the hostile backup test.

## Risks

- A later challenge store could write a credential row without the bound values of its challenge. The insert query takes each bound value as a parameter with a `NOT NULL` column, and the later change calls the same query in `CompleteChallenge`.
- A later caller could delete a TOTP factor or a key without the revoke. The port comments of `ConfirmTOTP`, `ResetTOTP`, `RevokeKey` and `ResetFactors` name the revoke, and their conformance cases check it.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Store adapter | Keep the SHA-256 hash of the secret, never the secret. | The row and each column hold no plain secret. |
| Store adapter | Take one use with a conditional update. | Two uses at the same time on the last use: one wins. |
| Store adapter | Check each bound value, and make the credential void on a mismatch. | A use with another person, holder, action, object, namespace, binding, job or plan digest fails, and a later good use fails with `stepup.ErrUsed`. |
| Store adapter | A revoke makes each credential of the key, the TOTP factor or the person void. | A use after the revoke fails with `stepup.ErrUsed`. |
| Restore | Leave the table empty. | A backup taken with a live credential, restored, and the credential is refused. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| correctness | 1 winner of two uses on the last use | calls | `UseCredential` |
| correctness | 0 credentials that work after a restore | credentials | restore |

## Contract

**stepup.CredentialStore**

- `UseCredential`: pre: the secret hash of the use is the hash of the secret. Post: on success, one use is taken and the credential returns; on `stepup.ErrMismatch` or `stepup.ErrFactorRefused` the credential is void and the failure entry of the use is appended in the same transaction; on `stepup.ErrNotFound`, `stepup.ErrExpired` or `stepup.ErrUsed` nothing changes.
- `RevokeCredentialsOfKey`, `RevokeCredentialsOfTOTP`, `RevokeCredentialsOfPerson`: pre: none. Post: each matching credential is void; other credentials do not change.

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Use | Two calls on the last use | The loser does the step-up again. | One call wins; `uses_left` is 0. |
| Use | The audit append fails on a mismatch | The caller retries the write. | The credential is not void; nothing is appended. |
| Revoke | The transaction of the caller rolls back | The caller retries. | No credential is void. |
| Restore | An older backup | None. | The table is empty. |
