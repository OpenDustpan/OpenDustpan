## ADDED Requirements

### Requirement: require-mfa-password-signin

While built-in MFA is on, the server SHALL create a pending sign-in and no session for a correct local or LDAP password when the person has a factor, when require MFA is on, or when an SSO provider exists and the password sign-in is on.

#### Scenario: Password only
- **WHEN** built-in MFA is on, require MFA is on and a person gives a correct password
- **THEN** the server answers 202, sets the pending cookie, creates a pending sign-in and issues no session

#### Scenario: Person with a factor, require MFA off
- **WHEN** built-in MFA is on, require MFA is off, and a person who has a factor gives a correct password
- **THEN** the server answers 202, sets the pending cookie, creates a pending sign-in and issues no session

#### Scenario: SSO provider next to the password sign-in
- **WHEN** an SSO provider exists, the password sign-in is on, built-in MFA is on, require MFA is off, and a person gives a correct password
- **THEN** the server answers 202, creates a pending sign-in and issues no session

### Requirement: sso-no-added-step

When a person signs in through an SSO provider, the server SHALL issue the session with no built-in second factor.

#### Scenario: SSO sign-in
- **WHEN** a person signs in through SSO
- **THEN** the server issues a session

### Requirement: fresh-step

While built-in MFA is on and the fresh step is on (`freshAuthEnabled`), when the last MFA step is older than the window, the server SHALL refuse token creation with error code mfa.fresh-step-required.

#### Scenario: Stale step
- **WHEN** the step is older than the window
- **THEN** the server answers 403 and creates no token

### Requirement: single-use-second-factor

When a person gives a second factor for a pending sign-in, the server SHALL accept a TOTP code one time only, with the single-use rule of the step-up TOTP factor.

#### Scenario: Valid code
- **WHEN** a person gives a valid TOTP code for a pending sign-in
- **THEN** the server issues a session through `CompletePending`

#### Scenario: Replayed code
- **WHEN** a person gives a TOTP code that the server already accepted
- **THEN** the server refuses it and issues no session

### Requirement: second-factor-route

The server SHALL take the second factor of a pending sign-in on one route, POST /api/dustpan/v1/sign-in/second-factor, with the pending cookie of the browser. The body is JSON with a field `method` and no other field than the one that the method names. The server SHALL answer 204 with the session cookie for a valid factor, and 401 with one text for every other case.

- `method` `totp`: the field `code` holds 6 digits.
- `method` `recovery-code`: the field `code` holds one recovery code.
- `method` `passkey-options`: no other field. The answer is 200 with the request options of an assertion ceremony that is bound to the pending sign-in.
- `method` `passkey`: the field `response` holds the response of the authenticator.

The 401 answer is {"code": 401, "message": "The second factor is not valid. Run `opendustpan status` and sign in again."}. The route is public. A request with no pending cookie, an expired or void pending sign-in, an unknown method, or a field that the method does not name gets the 401 answer. Each failure spends one unit of the sign-in budget of the source address, and each refusal appends an audit entry.

#### Scenario: Valid TOTP code
- **WHEN** a person gives a valid TOTP code with a pending cookie
- **THEN** the server answers 204 with the session cookie and deletes the pending sign-in

#### Scenario: No pending cookie
- **WHEN** a request has no pending cookie
- **THEN** the server answers 401 and issues no session

#### Scenario: Wrong factors until void
- **WHEN** a person gives 5 wrong factors
- **THEN** the pending sign-in is void and a valid factor then gets the 401 answer

### Requirement: pending-store-adapters

The SQLite and the PostgreSQL store SHALL implement `mfa.PendingStore`, `mfa.RecoveryCodeStore` and `stepup.TOTPStore`, and each adapter SHALL run the conformance suites of the ports. A store keeps the digest of a code or of a pending secret and the sealed TOTP secret, never a plain code or secret.

#### Scenario: Digest only
- **WHEN** a test reads each row of the three ports
- **THEN** no row holds a plain code or a plain pending secret

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
