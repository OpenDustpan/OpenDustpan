## ADDED Requirements

### Requirement: Merged audit trace

When the server commits a merge, the server SHALL write one merged audit entry with each claim ID.

#### Scenario: Merged audit entry
- **WHEN** a merge commits
- **THEN** the audit log holds one merged entry with each claim ID and each parent version ID committed by merge-core

#### Scenario: Three claim audit
- **WHEN** three disjoint claims merge
- **THEN** the merged entry holds three claim IDs

#### Scenario: Filter by claim
- **WHEN** a caller filters audit by claim ID
- **THEN** the audit read filtered by one claim ID returns the merged entry

### Requirement: Audit has no secrets

When the server writes the merged audit entry, the server SHALL exclude plain secret values.

#### Scenario: No plain secret in audit
- **WHEN** a merge combines sealed values
- **THEN** the merged entry holds only masked values and identifiers

### Requirement: Audit authorization

While serving the merged audit entry, the server SHALL check audit read action on the namespace.

#### Scenario: Denied audit read
- **WHEN** a caller without audit read action requests the merged entry
- **THEN** the server denies the read with 403
