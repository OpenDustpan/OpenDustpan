## ADDED Requirements

### Requirement: Save namespace trust bundle

When an administrator saves a trust bundle, the server SHALL store it as a new version under that namespace only.

#### Scenario: Save valid bundle
- **WHEN** administrator saves a PEM bundle with unexpired CAs in one namespace
- **THEN** server stores a new bundle version, keeps the prior version, and writes an audit entry

### Requirement: Refuse bad CA

If a bundle holds malformed PEM or an expired CA, then the server SHALL refuse the save.

#### Scenario: Refuse expired CA
- **WHEN** bundle holds malformed PEM or an expired CA
- **THEN** server refuses the save with 400, names the CA index, and stores nothing

### Requirement: Fresh auth for bundle save

When an administrator saves a trust bundle, the server SHALL require a fresh authentication.

#### Scenario: Stale bundle save refused
- **WHEN** bundle save arrives with an authentication older than freshAuthSeconds
- **THEN** server refuses with fresh-authentication-required

### Requirement: Refuse active version delete

If a caller deletes the active bundle version, then the server SHALL refuse the delete.

#### Scenario: Active delete refused
- **WHEN** caller deletes the active bundle version
- **THEN** server refuses with 409 and keeps the active version trusted

### Requirement: Concurrent bundle save

When two bundle saves race, the server SHALL order versions and refuse a stale If-Match.

#### Scenario: Stale version refused
- **WHEN** two saves race and the second carries a stale ETag in If-Match
- **THEN** server refuses with 412 and stores no new version

### Requirement: Bounded rotation overlap

When a rotation opens an overlap window, the server SHALL end the overlap at the stated time.

#### Scenario: Overlap ends
- **WHEN** the overlap window ends
- **THEN** server trusts only the new active version and shows the end in the read
