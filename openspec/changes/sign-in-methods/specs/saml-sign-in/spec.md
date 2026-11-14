## ADDED Requirements

### Requirement: SP-started SAML sign-in

When a person starts a SAML sign-in, the server SHALL send a request with a stored request ID.

#### Scenario: Browser round trip
- **WHEN** the identity provider returns a valid response to a stored request ID
- **THEN** the server opens a session with principal source `saml` and writes an audit entry.

### Requirement: Assertion checks

If an assertion fails the audience, recipient, time or signature check, then the server SHALL refuse the sign-in.

#### Scenario: Wrong audience
- **WHEN** the assertion names another audience
- **THEN** the server gives 401 with one generic answer and writes an audit entry.

#### Scenario: Expired assertion
- **WHEN** the assertion is older than the maximum age
- **THEN** the server gives 401 and opens no session.

### Requirement: No replay

If a response uses a request ID or assertion ID that is consumed or expired, then the server SHALL refuse the sign-in.

#### Scenario: Replayed ID
- **WHEN** the same assertion arrives twice
- **THEN** the second use gives 401 and an audit entry.

#### Scenario: Concurrent use
- **WHEN** two requests carry the same assertion at the same time
- **THEN** one request opens a session and the other gives 401.

### Requirement: Store outage

If the one-time ID store is not available, then the server SHALL refuse the sign-in.

#### Scenario: Database down
- **WHEN** the store fails during the ID check
- **THEN** the server gives 401 and opens no session.

### Requirement: IdP-started off

While the IdP-started setting is off, the server SHALL refuse an unsolicited assertion.

#### Scenario: Unsolicited refused
- **WHEN** an assertion arrives with no stored request
- **THEN** the server gives 401.

### Requirement: IdP-started never fresh

Where IdP-started SAML is on, the server SHALL mark no such sign-in as a fresh authentication.

#### Scenario: Fresh step refused
- **WHEN** a person who signed in IdP-started asks for a step that needs a fresh authentication
- **THEN** the server asks for a new SP-started sign-in.

### Requirement: No group claim

If a SAML assertion carries a group claim, then the server SHALL apply no group from it.

#### Scenario: Group ignored
- **WHEN** an assertion holds a group attribute
- **THEN** the person signs in and holds no membership from the claim.
