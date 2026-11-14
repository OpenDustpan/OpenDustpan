## ADDED Requirements

### Requirement: Save namespace trust bundle (save-bundle)

When an administrator saves a trust bundle, the server SHALL store it as a new version under that namespace only.

#### Scenario: Save valid bundle
- **WHEN** an administrator with the namespace action and a fresh session runs save-bundle with a valid PEM bundle
- **THEN** the server stores a new version, keeps the prior version and writes an audit entry

### Requirement: Refuse invalid bundle

If a bundle holds malformed PEM, an expired CA or input outside the size bounds, then the server SHALL refuse the save.

#### Scenario: Refuse expired CA
- **WHEN** a bundle holds an expired CA
- **THEN** the server refuses with 400, names the CA index and stores nothing

### Requirement: Rotate bundle (rotate-bundle)

When an administrator rotates the bundle, the server SHALL move the active pointer to the new version.

#### Scenario: Pointer moves
- **WHEN** an administrator runs rotate-bundle with a valid bundle and an overlap of at most 24 hours
- **THEN** the server marks the new version active and states the overlap end

### Requirement: Refuse active version delete

If a caller deletes the active bundle version, then the server SHALL refuse the delete.

#### Scenario: Active delete refused
- **WHEN** a caller deletes the active version
- **THEN** the server refuses with 409

### Requirement: Fresh authentication for bundle write

If a bundle write arrives without a fresh session authentication, then the server SHALL refuse the write.

#### Scenario: Token only refused
- **WHEN** a save arrives with an access token only
- **THEN** the server refuses with 403
