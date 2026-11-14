## ADDED Requirements

### Requirement: CAS subject attribute

If a CAS provider has no named subject attribute, then the server SHALL refuse the sign-in.

#### Scenario: Plain user name
- **WHEN** the CAS answer holds only the plain user name
- **THEN** the server gives 401 and writes an audit entry.

#### Scenario: Named attribute
- **WHEN** the CAS answer holds the named attribute
- **THEN** the server makes or finds the person with principal source `cas` and opens a session.
