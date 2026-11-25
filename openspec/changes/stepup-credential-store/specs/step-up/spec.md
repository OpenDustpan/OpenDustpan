## ADDED Requirements

### Requirement: credential-store-adapters

The SQLite and the PostgreSQL store SHALL implement `stepup.CredentialStore`, and each adapter SHALL run the conformance cases of the port that need no other step-up port. A store keeps the SHA-256 hash of the secret of a credential, never the secret.

#### Scenario: Hash only
- **WHEN** a test reads each row of the credential table
- **THEN** no row holds the plain secret of a credential

### Requirement: credential-use-is-single

A use of a step-up credential SHALL take one use in one transaction, and of two uses at the same time on the last use, one SHALL win and the other SHALL fail with `stepup.ErrUsed`.

#### Scenario: Last use
- **WHEN** two calls use a credential with one use left at the same time
- **THEN** one call returns the credential and the other fails with `stepup.ErrUsed`

#### Scenario: Write credential
- **WHEN** a write credential is used one time and then used again
- **THEN** the second use fails with `stepup.ErrUsed`

### Requirement: credential-mismatch-voids

A use whose person, holder, action, object, namespace, binding, job or plan digest differs from the credential, or whose level is above the level of the credential, SHALL fail, SHALL make the credential void and SHALL append the failure entry of the use, in one transaction.

#### Scenario: Another action
- **WHEN** a credential is used for another action
- **THEN** the use fails with `stepup.ErrMismatch` and the audit log has the failure entry
- **AND** a later use with the right action fails with `stepup.ErrUsed`

### Requirement: credential-revoke

A revoke of the credentials of a key, of a TOTP factor or of a person SHALL make each matching credential void and SHALL leave each other credential working. Each revoke SHALL run inside the transaction of its caller when a store calls it.

#### Scenario: TOTP factor revoked
- **WHEN** the credentials of a TOTP factor are revoked
- **THEN** a use of a credential of that factor fails with `stepup.ErrUsed`
- **AND** a credential of a key of the same person still works

### Requirement: restore-ends-credentials

A backup SHALL carry no step-up credential, and a restore SHALL leave the credential table empty.

#### Scenario: Restore of a backup
- **WHEN** a backup is taken while a credential is live and the backup is restored
- **THEN** a use of the credential fails with `stepup.ErrNotFound`
