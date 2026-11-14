## ADDED Requirements

### Requirement: Scoped repo token

The server SHALL mint each repo token for one repository with contents read and an expiry of at most 1 hour.

#### Scenario: Scoped token
- **WHEN** the server mints a repo token
- **THEN** the token has one repository and an expiry within 1 hour

#### Scenario: Unscoped token refused
- **WHEN** a token with Scoped not set reaches the runner git path
- **THEN** the runner path refuses the token
