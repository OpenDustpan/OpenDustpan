## ADDED Requirements

### Requirement: pending-store-adapters

The SQLite and the PostgreSQL store SHALL implement `mfa.PendingStore`, `mfa.RecoveryCodeStore` and `stepup.TOTPStore`, and each adapter SHALL run the conformance suites of the ports. A store keeps the digest of a code or of a pending secret and the sealed TOTP secret, never a plain code or secret.

#### Scenario: Digest only
- **WHEN** a test reads each row of the three ports
- **THEN** no row holds a plain code or a plain pending secret

### Requirement: completion-value-rejects-incomplete-parts

The pending sign-in port SHALL take the session, the digest of its secret and the audit entry of a completion as one value, and the only constructor of that value SHALL return `ErrInvalid` for a zero session ID, a zero digest or an entry with no action.

#### Scenario: Zero session ID
- **WHEN** a caller builds a completion with a session that has no ID
- **THEN** the constructor returns `ErrInvalid`

#### Scenario: Zero digest
- **WHEN** a caller builds a completion with a zero digest
- **THEN** the constructor returns `ErrInvalid`

#### Scenario: Entry with no action
- **WHEN** a caller builds a completion with an audit entry that has no action
- **THEN** the constructor returns `ErrInvalid`

### Requirement: sign-in-step-use-keeps-step-up-counters

The TOTP port SHALL have one method that accepts a time step for a sign-in with the single-use rule of the step-up, and the method SHALL change no step-up counter, lock or disable.

#### Scenario: Step-up lockout
- **WHEN** the step-up of a person is locked and the person gives a valid TOTP code at the sign-in
- **THEN** the server accepts the code and issues a session

#### Scenario: Wrong sign-in codes
- **WHEN** a person gives wrong TOTP codes at the sign-in
- **THEN** the step-up failure count of the person does not change

### Requirement: restore-keeps-second-factor-safe

A restore SHALL NOT make a used recovery code valid again, SHALL NOT end a lock of a TOTP factor or make a used step valid again, and SHALL leave no TOTP attempts row of a factor that the restored data does not have. A backup SHALL carry no recovery code, and a restore that replaces access SHALL empty the recovery codes and write one audit entry that names the reason.

#### Scenario: Older backup and a used recovery code
- **WHEN** a person uses a recovery code and a restore of an older backup replaces access
- **THEN** the code is refused and the audit log has one entry that names the removal of the recovery codes

#### Scenario: Restore during a lock
- **WHEN** a TOTP factor is locked and a restore of an older backup runs
- **THEN** the lock holds after the restore
- **AND** a step that the person used before the restore stays refused

#### Scenario: Factor not in the backup
- **WHEN** a restore replaces access and the restored data does not have a factor that has a TOTP attempts row
- **THEN** no attempts row of that factor stays and the audit log has one entry that gives the count
