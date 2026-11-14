## ADDED Requirements

### Requirement: Mint with agent key

When a caller with mint rights mints a job token with the NEW agent key, the server SHALL store the key with the token record and return it on verification.

#### Scenario: Mint token with agent key
- **WHEN** a caller with mint rights mints a job token with the NEW agent key that names an existing agent-kind principal
- **THEN** verification returns the same agent key with the existing claims unchanged.

### Requirement: Reject bad agent key

If a caller mints a job token with a malformed NEW agent key or a key that names no existing agent-kind principal, then the server SHALL reject the mint with the NEW error code jobtoken.agent-invalid.

#### Scenario: Reject overlong agent key
- **WHEN** a caller mints a job token with a malformed agent key, a key longer than 256 characters, or a key that names no existing agent-kind principal
- **THEN** the server rejects the mint with the NEW error code jobtoken.agent-invalid.

### Requirement: Read legacy job token

When a stored job token carries no agent key, the server SHALL verify it with existing claims unchanged.

#### Scenario: Verify token without key
- **WHEN** a stored job token carries no agent key
- **THEN** verification passes with no agent key and existing claims unchanged.
