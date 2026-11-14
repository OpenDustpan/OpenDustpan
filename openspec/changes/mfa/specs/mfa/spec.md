## ADDED Requirements

### Requirement: require-mfa-password-signin

When require MFA is on, the server SHALL create a pending sign-in and no session for a correct local or LDAP password.

#### Scenario: Password only
- **WHEN** require MFA is on and a person gives a correct password
- **THEN** the server creates a pending sign-in and issues no session

### Requirement: sso-no-added-step

When a person signs in through an SSO provider, the server SHALL issue the session with no built-in second factor.

#### Scenario: SSO sign-in
- **WHEN** a person signs in through SSO
- **THEN** the server issues a session

### Requirement: fresh-step

While built-in MFA is on, when the last MFA step is older than the window, the server SHALL refuse token creation with error code mfa.fresh-step-required.

#### Scenario: Stale step
- **WHEN** the step is older than the window
- **THEN** the server answers 403 and creates no token
