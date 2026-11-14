## ADDED Requirements

### Requirement: Bound manifest bytes

When an untrusted backup manifest exceeds 10 MB of total reads per manifest, the server SHALL refuse the manifest after bounded reads.

#### Scenario: Oversized manifest
- **WHEN** a manifest exceeds 10 MB
- **THEN** the server refuses with request.body-too-large and writes no partial state.

#### Scenario: Exactly at limit
- **WHEN** a manifest holds exactly 10 MB
- **THEN** the server accepts and decodes the manifest.

#### Scenario: Lying length across reads
- **WHEN** a manifest states a small length but sends more than 10 MB across reads
- **THEN** the server refuses with request.body-too-large after bounded reads.

### Requirement: Bound unverified keys

When a backup manifest holds more than 100000 unverified blob keys per manifest, the server SHALL refuse the manifest without weakening restore completeness checks.

#### Scenario: Many unverified keys
- **WHEN** a manifest holds more than 100000 unverified blob keys
- **THEN** the server refuses the manifest.

#### Scenario: Exactly at key bound
- **WHEN** a manifest holds exactly 100000 unverified blob keys
- **THEN** the server accepts the manifest when completeness checks pass.
