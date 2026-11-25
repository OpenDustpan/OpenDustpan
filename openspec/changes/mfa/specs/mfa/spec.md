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
