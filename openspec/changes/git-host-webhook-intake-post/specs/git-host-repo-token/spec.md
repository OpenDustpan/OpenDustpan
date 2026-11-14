## ADDED Requirements

### Requirement: Scoped repo token

The server SHALL mint RepoToken values scoped to one repository with contents read and an expiry of at most 1 hour.

#### Scenario: Mint scoped token
- **WHEN** the server mints a RepoToken for one repository
- **THEN** the token is scoped to one repository with contents read and an expiry within 1 hour

#### Scenario: Unscoped token refused
- **WHEN** a caller passes a token with Scoped not set to the runner git credential path
- **THEN** the runner path refuses the token

#### Scenario: Expired token refused
- **WHEN** a caller passes a repo token after its one-hour expiry
- **THEN** the runner path refuses the token

#### Scenario: Host refuses token mint
- **WHEN** the Git host answers RepoToken with an auth failure or a rate limit
- **THEN** the server returns a refusal that names the host and the one check command
