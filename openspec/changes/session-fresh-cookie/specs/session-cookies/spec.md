## ADDED Requirements

### Requirement: A sign-in carries its fresh fact in a sealed cookie

At each sign-in that starts a console session of a person, the server SHALL set the access cookie with the kind and the time of the fresh authentication of this sign-in, and the time of the last MFA step. A method that gives no step SHALL leave them empty. A renewal of a session after a password change is not a sign-in and SHALL set an access cookie with no fresh fact. An SSO authentication time that is unknown, in the future or older than the freshness window SHALL give no fact. A request SHALL never change them. The server SHALL read the cookie only after the stored session authenticated the request, and SHALL use its fact only when the principal, the generation and the sign-in time match the stored session.

#### Scenario: Passkey sign-in
- **WHEN** a person signs in with a passkey
- **THEN** the access cookie has the kind `passkey` and the time of the sign-in

#### Scenario: Cookie of another person
- **WHEN** a request has the stored session of one person and the access cookie of another
- **THEN** the request has no fresh fact and the request itself is not refused

#### Scenario: Re-issue
- **WHEN** a request near the expiry gets a new access cookie
- **THEN** the kind and the times are the same as before

#### Scenario: Renewal after a password change
- **WHEN** a person changes a password and the session renews
- **THEN** the new access cookie has no kind and no times

#### Scenario: SSO time in the future
- **WHEN** the provider gives an authentication time in the future
- **THEN** the access cookie has no kind and no times

#### Scenario: Re-issue of an unbound cookie
- **WHEN** a request has an access cookie that does not match the stored session
- **THEN** the server does not re-issue it
