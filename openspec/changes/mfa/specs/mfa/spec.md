## ADDED Requirements

### Requirement: mfa-off-by-default

While MFA is off, the server SHALL ask for no second factor.

#### Scenario: Default installation asks no second factor
- **WHEN** an administrator did not turn MFA on
- **THEN** the server issues a session from the password alone

### Requirement: require-mfa-local-signin

When require MFA is on, the server SHALL refuse a local password sign-in with no valid second factor.

#### Scenario: local password without second factor
- **WHEN** require MFA is on and a local user signs in with a password only
- **THEN** the server answers 401 and creates a pending sign-in with no session

#### Scenario: local password plus TOTP
- **WHEN** require MFA is on and the user gives a valid TOTP code as second factor
- **THEN** the server issues a session

#### Scenario: wrong second factor
- **WHEN** the user gives a wrong TOTP code or a failed WebAuthn assertion
- **THEN** the server answers 401 and counts the failure under the factor lock

#### Scenario: reused TOTP code
- **WHEN** the user sends a TOTP code that an earlier check already used
- **THEN** the server answers 401 and issues no session

### Requirement: require-mfa-ldap-signin

When require MFA is on, the server SHALL refuse an LDAP password sign-in with no valid second factor.

#### Scenario: LDAP password without second factor
- **WHEN** require MFA is on and an LDAP user signs in with a password only
- **THEN** the server answers 401 and creates a pending sign-in with no session

#### Scenario: LDAP password plus WebAuthn
- **WHEN** require MFA is on and the user gives a valid WebAuthn assertion with user verification preferred as second factor
- **THEN** the server issues a session

### Requirement: sso-no-added-step

When a person signs in through an SSO provider, the server SHALL issue the session with no built-in second factor.

#### Scenario: SSO sign-in completes
- **WHEN** a person signs in through an SSO provider
- **THEN** the server issues a session with no built-in second factor

#### Scenario: Local password while SSO exists
- **WHEN** both an SSO provider and local password sign-in exist
- **THEN** the server applies the SSO-exists condition of the local password rule

### Requirement: token-use-no-step

When a tool sends a valid bearer token, the server SHALL ask for no MFA step.

#### Scenario: CLI use of a valid token
- **WHEN** a tool sends a valid bearer token
- **THEN** the server allows the request with no MFA step

### Requirement: fresh-step-token-and-reveal

While built-in MFA covers the operation, when the last MFA step is older than the fresh-authentication window, the server SHALL refuse token creation, token rotation, and secret reveal where a reveal needs a fresh step.

#### Scenario: fresh step inside window succeeds
- **WHEN** built-in MFA covers the operation, the last MFA step is inside the window, and the person requests token creation at POST /api/dustpan/v1/me/tokens, token rotation, or secret reveal where a reveal needs a fresh step
- **THEN** the server creates the token and reveals the value

#### Scenario: stale step for token creation
- **WHEN** built-in MFA covers the operation, the last MFA step is older than the window, and the person creates or rotates an access token
- **THEN** the server answers 403 with error code mfa.fresh-step-required and creates no token

#### Scenario: stale step for reveal
- **WHEN** built-in MFA covers the operation, the last MFA step is older than the window, and the person reveals a secret value where a reveal needs a fresh step
- **THEN** the server answers 403 with error code mfa.fresh-step-required and reveals nothing

#### Scenario: fresh step from another sign-in
- **WHEN** token creation or reveal presents a sealed cookie from a different sign-in
- **THEN** the server answers 403 with error code mfa.fresh-step-required and changes nothing

#### Scenario: MFA off asks no fresh step
- **WHEN** built-in MFA does not cover the operation
- **THEN** the server creates the token and reveals the value with no fresh step

### Requirement: enrollment-recovery

At enrollment the server SHALL show recovery codes one time.

#### Scenario: Enroll TOTP
- **WHEN** a person enrolls a TOTP factor
- **THEN** the server shows recovery codes one time, writes an audit entry, and sends a notice to the person

### Requirement: recovery-code-signin

When a person gives an unused recovery code as the second factor, the server SHALL issue a session.

#### Scenario: Sign in with a recovery code
- **WHEN** a person with a lost factor gives an unused recovery code as the second factor
- **THEN** the server issues a session, marks that code used, and writes an audit entry

#### Scenario: Reused recovery code
- **WHEN** a person gives a recovery code that an earlier sign-in already used
- **THEN** the server answers 401 and counts the failure under the factor lock

### Requirement: admin-reset

On administrator reset the server SHALL clear the MFA factors of the user.

#### Scenario: Reset lost factors
- **WHEN** an administrator resets the MFA of a user
- **THEN** the server clears the factors, writes an audit entry, and sends a notice to the person

### Requirement: reveal-setting

When the separate secret-reveal MFA setting is on, the server SHALL require a fresh step inside the one fresh-authentication window for a secret reveal.

#### Scenario: Separate reveal setting on
- **WHEN** the separate secret-reveal MFA setting is on
- **THEN** the server requires a fresh step inside the one window for the reveal

#### Scenario: Separate reveal setting off
- **WHEN** the separate secret-reveal MFA setting is off
- **THEN** the server applies only the require MFA rule for sign-in and token creation

### Requirement: pending-signin-limits

While a sign-in is pending, the server SHALL refuse each request that needs a session.

#### Scenario: Pending sign-in used as session
- **WHEN** a client sends a pending sign-in where a session is required
- **THEN** the server refuses the request

#### Scenario: No-longer-valid pending sign-in
- **WHEN** the pending sign-in already completed or is no longer valid
- **THEN** the server refuses the second-factor check

#### Scenario: Second completion races the first
- **WHEN** two second-factor checks use the same pending sign-in at the same time
- **THEN** the server completes one and refuses the other as no longer valid

#### Scenario: Restart during pending sign-in
- **WHEN** the server restarts before the person gives the second factor
- **THEN** the server verifies the sealed pending cookie with no stored server state

#### Scenario: Generation bump during sign-in
- **WHEN** the session generation of the principal grows before completion
- **THEN** the server refuses the old cookie
