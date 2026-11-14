## ADDED Requirements

### Requirement: Signature check

When a SAML response arrives, the server SHALL refuse it unless a pinned certificate of the provider signs it.

#### Scenario: Unpinned signature
- **WHEN** a response is signed by a certificate that is not pinned
- **THEN** the server gives 401 with code `sign-in.failed` and writes an audit entry.

#### Scenario: Wrapped signature
- **WHEN** a response holds a wrapped signed element
- **THEN** the server gives 401 with code `sign-in.failed`.

### Requirement: Unsolicited assertion

Where IdP-started sign-in is on, the server SHALL accept an assertion only with a one-time ID, an age of 5 minutes at most, the right audience and the right recipient.

#### Scenario: Valid assertion
- **WHEN** an unsolicited assertion passes all four checks
- **THEN** the server opens a session that is not a fresh authentication.

#### Scenario: Wrong audience
- **WHEN** an unsolicited assertion names another audience
- **THEN** the server gives 401 with code `sign-in.failed`.

#### Scenario: Old assertion
- **WHEN** an unsolicited assertion is older than 5 minutes
- **THEN** the server gives 401 with code `sign-in.failed`.
