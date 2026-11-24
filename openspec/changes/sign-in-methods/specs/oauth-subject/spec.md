## ADDED Requirements

### Requirement: Subject from sub only

The server SHALL take the subject of a plain OAuth 2.0 person from the `sub` claim of the user-info answer only, and SHALL use the display claims for display fields only.

#### Scenario: Subject claim setting refused
- **WHEN** an administrator creates or changes a plain OAuth provider with `claims.subject`
- **THEN** the server gives 400 and stores no change.

#### Scenario: Display claims do not identify
- **WHEN** the user-info answer has no `sub` claim but has `id`, `name`, `preferred_username` and `email`
- **THEN** the server refuses the sign-in and does not use another claim as the subject.
