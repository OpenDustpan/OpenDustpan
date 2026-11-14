## ADDED Requirements

### Requirement: Disabled person

If a person is disabled, then the server SHALL refuse a sign-in by any method with one generic answer.

#### Scenario: Disabled person
- **WHEN** the identity provider proves a disabled person
- **THEN** the server gives 401 with code `signin.failed`, opens no session and writes an audit entry with the reason.
