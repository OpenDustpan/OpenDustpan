## ADDED Requirements

### Requirement: Protected member issue needs cover

When a token create or rotation targets a principal in a protected group, the server SHALL refuse the issue unless the reach of the caller covers the reach of each such group.

#### Scenario: Non-covering create refused
- **WHEN** the caller reach does not cover the protected group
- **THEN** the server answers 403, names the principal and the group, and issues no token

#### Scenario: Non-covering rotation refused
- **WHEN** the caller rotates a token without cover
- **THEN** the server answers 403 and keeps the old token

#### Scenario: Group protected later
- **WHEN** the principal belongs to a group that became protected after its grant
- **THEN** the server applies the check at the next issue

#### Scenario: Unprotected member allowed
- **WHEN** the principal belongs to no protected group
- **THEN** the server issues the token after its other checks
