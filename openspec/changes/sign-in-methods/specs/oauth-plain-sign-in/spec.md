## ADDED Requirements

### Requirement: Subject is sub

When a person signs in through a plain OAuth 2.0 provider, the server SHALL take identity only from the `sub` claim of the user-info answer.

#### Scenario: User-info sub
- **WHEN** the user-info answer holds `sub`
- **THEN** the server makes or finds the person with source `oauth` and opens a session.

### Requirement: Missing sub

If the user-info answer has no `sub` claim, then the server SHALL refuse the sign-in.

#### Scenario: Sub absent
- **WHEN** a known person returns a user-info answer with no `sub`
- **THEN** the server gives 401, opens no session and writes an audit entry.

### Requirement: User-info failure

If the user-info call fails, times out or returns a claim of the wrong type, then the server SHALL refuse the sign-in.

#### Scenario: Non-200 answer
- **WHEN** the user-info endpoint answers 500
- **THEN** the server gives 401 and no session.

### Requirement: No group claim

If a user-info answer carries a group claim, then the server SHALL apply no group from it.

#### Scenario: Group ignored
- **WHEN** the answer holds a groups array
- **THEN** the person signs in and holds no membership from it.
