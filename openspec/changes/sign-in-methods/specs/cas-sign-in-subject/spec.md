## ADDED Requirements

### Requirement: CAS subject from a named attribute

The server SHALL take the subject of a CAS person from the attribute that the provider names, and SHALL refuse a CAS provider without a subject attribute.

#### Scenario: Provider without a subject attribute
- **WHEN** an administrator creates a CAS provider without a subject attribute
- **THEN** the server gives 400 and stores no provider.

#### Scenario: Sign-in with the attribute
- **WHEN** a CAS validation answer has a value for the subject attribute of the provider
- **THEN** the server opens a session for the subject `<provider ID>:<attribute value>`, whatever the CAS user is.
