## ADDED Requirements

### Requirement: Save namespace trust bundle

When an administrator saves a trust bundle, the server SHALL store it as a new version under that namespace only.

#### Scenario: Save valid bundle
- **WHEN** administrator with the namespace action and fresh session runs save-bundle with a valid PEM bundle
- **THEN** server stores a new version, keeps the prior version and writes an audit entry

### Requirement: Refuse invalid bundle

If a bundle holds malformed PEM, an expired CA, or input outside the size bounds, then the server SHALL refuse the save.

#### Scenario: Refuse expired CA
- **WHEN** bundle holds malformed PEM or an expired CA
- **THEN** server refuses with 400, names the CA index and stores nothing

### Requirement: Rotate bundle

When an administrator runs rotate-bundle, the server SHALL add a new version and move the active pointer to it.

#### Scenario: Pointer moves
- **WHEN** administrator runs rotate-bundle with a valid bundle and an overlap of at most 24 hours
- **THEN** server marks the new version active and states the overlap end

### Requirement: Refuse active version delete

If a caller deletes the active bundle version, then the server SHALL refuse the delete.

#### Scenario: Active delete refused
- **WHEN** caller deletes the active version
- **THEN** server refuses with 409

### Requirement: Fresh auth for bundle save

When an administrator saves or rotates a trust bundle, the server SHALL require a fresh session authentication.

#### Scenario: Token-only refused
- **WHEN** save arrives with an access token only
- **THEN** server refuses with 403 fresh-authentication-required
