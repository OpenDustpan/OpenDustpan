## ADDED Requirements

### Requirement: refuse-empty-source

If the principal source is empty, then the server SHALL refuse the sign-in with principal.invalid.

#### Scenario: Empty source sign-in
- **WHEN** Service.signIn is called with an empty principal source
- **THEN** it refuses the sign-in with principal.invalid and creates no principal.

#### Scenario: Valid source still signs in
- **WHEN** Service.signIn is called with a non-empty valid source
- **THEN** it creates or matches the principal for that source.
