## ADDED Requirements

### Requirement: Replay refusal

When a browser sign-in reuses a state, a ticket or a request ID, the server SHALL refuse the sign-in.

#### Scenario: CAS ticket replay
- **WHEN** a CAS ticket is used twice
- **THEN** the second use gives 401 with code `sign-in.failed`.

#### Scenario: OAuth state replay
- **WHEN** an OAuth callback repeats a used state
- **THEN** the server gives 401 with code `sign-in.failed`.

### Requirement: Store outage

If the one-time ID store is unavailable, then the server SHALL refuse the sign-in.

#### Scenario: Outage
- **WHEN** the store fails during an OIDC, LDAP, SAML, CAS or passkey sign-in
- **THEN** the server gives 503 with code `sign-in.unavailable` and opens no session.
