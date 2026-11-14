## ADDED Requirements

### Requirement: mfa-off-by-default

While MFA is off, the server SHALL ask for no second factor.

#### Scenario: Default installation asks no second factor
- **WHEN** an administrator did not turn MFA on
- **THEN** the server issues a session from the password alone

### Requirement: require-mfa-local-signin

When require MFA is on, the server SHALL create a pending sign-in with no session for a local password first factor.

#### Scenario: Local password without second factor
- **WHEN** require MFA is on and a local user signs in with a correct password only
- **THEN** the server creates a pending sign-in and issues no session

#### Scenario: Local password plus TOTP
- **WHEN** require MFA is on and the user gives a valid TOTP code as second factor
- **THEN** the server issues a session

#### Scenario: Wrong second factor
- **WHEN** the user gives a wrong TOTP code or a failed WebAuthn assertion
- **THEN** the server answers 401 and counts the failure under the factor lock

#### Scenario: Reused TOTP code
- **WHEN** the user sends a TOTP code that an earlier check already used
- **THEN** the server refuses the check and issues no session

### Requirement: require-mfa-ldap-signin

When require MFA is on, the server SHALL create a pending sign-in with no session for an LDAP password first factor.

#### Scenario: LDAP password without second factor
- **WHEN** require MFA is on and an LDAP user signs in with a correct password only
- **THEN** the server creates a pending sign-in and issues no session

### Requirement: second-factor-verification

When a person gives a valid second factor for a pending sign-in, the server SHALL issue a session through VerifySecondFactor.

#### Scenario: VerifySecondFactor with valid factor
- **WHEN** a person gives a valid second factor for a pending sign-in
- **THEN** the server issues a session and ends the pending state

#### Scenario: VerifySecondFactor with reused factor
- **WHEN** a person gives a reused second factor
- **THEN** the server issues no session and counts the failure

### Requirement: sso-no-added-step

When a person signs in through an SSO provider, the server SHALL issue the session with no built-in second factor.

#### Scenario: SSO sign-in completes
- **WHEN** a person signs in through an SSO provider
- **THEN** the server issues a session with no built-in second factor

### Requirement: token-use-no-step

When a tool sends a valid bearer token, the server SHALL ask for no MFA step.

#### Scenario: CLI use of a valid token
- **WHEN** a tool sends a valid bearer token
- **THEN** the server allows the request with no MFA step

### Requirement: fresh-step-token-and-reveal

While built-in MFA covers the operation, when the last MFA step is older than the fresh-authentication window, the server SHALL refuse token creation, token rotation, and secret reveal where a reveal needs a fresh step.

#### Scenario: Stale step for token creation
- **WHEN** the last MFA step is older than the window and the person creates or rotates an access token
- **THEN** the server answers 403 with error code mfa.fresh-step-required and creates no token

#### Scenario: Stale step for reveal
- **WHEN** the last MFA step is older than the window and the person reveals a secret value
- **THEN** the server answers 403 with error code mfa.fresh-step-required and reveals nothing

#### Scenario: MFA off asks no fresh step
- **WHEN** built-in MFA does not cover the operation
- **THEN** the server creates the token and reveals the value with no fresh step

### Requirement: fresh-step-check

When an operation needs a fresh step, the server SHALL run CheckFreshStep on the sealed access cookie of the sign-in.

#### Scenario: CheckFreshStep with cookie of another sign-in
- **WHEN** the operation presents a sealed cookie from a different sign-in
- **THEN** the server answers 403 with error code mfa.fresh-step-required and changes nothing

#### Scenario: CheckFreshStep with fresh step of the same sign-in
- **WHEN** the operation presents a fresh step of the same sign-in
- **THEN** the server allows the operation

### Requirement: enrollment-recovery

At enrollment the server SHALL show recovery codes one time.

#### Scenario: Enroll TOTP
- **WHEN** a person enrolls a TOTP factor
- **THEN** the server shows recovery codes one time, writes an audit entry, and sends a notice to the person

### Requirement: recovery-code-signin

When a person gives an unused recovery code as the second factor, the server SHALL issue a session.

#### Scenario: Sign in with a recovery code
- **WHEN** a person gives an unused recovery code
- **THEN** the server issues a session, marks that code used, and writes an audit entry

#### Scenario: Reused recovery code
- **WHEN** a person gives a used recovery code
- **THEN** the server answers 401 and counts the failure under the factor lock

### Requirement: admin-reset

On administrator reset the server SHALL clear the MFA factors of the user.

#### Scenario: Reset lost factors
- **WHEN** an administrator with installation:manage-sign-in resets the MFA of a user
- **THEN** the server clears the factors, adds 1 to the session generation, writes an audit entry, and sends a notice to the person

### Requirement: reveal-setting

When the separate secret-reveal MFA setting is on, the server SHALL require a fresh step inside the one fresh-authentication window for a secret reveal.

#### Scenario: Separate reveal setting on
- **WHEN** the separate secret-reveal MFA setting is on
- **THEN** the server requires a fresh step for the reveal

### Requirement: pending-signin-limits

While a sign-in is pending, the server SHALL refuse each request that needs a session.

#### Scenario: Pending sign-in used as session
- **WHEN** a client sends a pending sign-in where a session is required
- **THEN** the server refuses the request
