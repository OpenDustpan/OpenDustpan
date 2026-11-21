## ADDED Requirements

### Requirement: Save namespace trust bundle

When an administrator runs save-bundle, the server SHALL store the bundle as a new version under that namespace only.

#### Scenario: Save valid bundle
- **WHEN** an administrator with the namespace action and a fresh session posts a valid PEM bundle
- **THEN** the server stores a new version, keeps the prior version and writes an audit entry

### Requirement: Refuse invalid bundle

If a bundle holds malformed PEM, an expired CA or input outside the size bounds, then the server SHALL refuse the save.

#### Scenario: Refuse expired CA
- **WHEN** a bundle holds an expired CA
- **THEN** the server refuses with 400, names the CA index and stores nothing

### Requirement: Rotate bundle

When an administrator runs rotate-bundle with PATCH, the server SHALL move the active pointer to the new version.

#### Scenario: Pointer moves
- **WHEN** an administrator patches with a valid bundle, a current If-Match and an overlap of at most 24 hours
- **THEN** the server marks the new version active and states overlapEndsAt

### Requirement: Refuse stale bundle patch

If a bundle PATCH carries a stale If-Match, then the server SHALL refuse it.

#### Scenario: Stale patch
- **WHEN** the If-Match differs from the revision
- **THEN** the server refuses with 412 and keeps the active version

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
