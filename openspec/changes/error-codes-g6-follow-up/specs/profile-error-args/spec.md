## ADDED Requirements

### Requirement: Count limit argument

When a profile count check fails, the server SHALL carry limit as a quantity argument.

#### Scenario: Count breach on both stores
- **WHEN** a profile count check fails on the sqlite or postgres store
- **THEN** the answer carries code profile.limit with errorArgs limit.

#### Scenario: Wrap keeps argument
- **WHEN** a store wraps the typed error with fmt.Errorf and %w
- **THEN** the answer still carries limit.

### Requirement: Byte limit argument

When a profile document exceeds its byte bound, the server SHALL carry limitBytes as a quantity argument.

#### Scenario: Byte breach
- **WHEN** a profile document exceeds its byte bound
- **THEN** the answer carries the NEW code profile.too-large with errorArgs limitBytes.

### Requirement: Schema version argument

When a profile document has a schemaVersion above the read maximum, the server SHALL carry maxSchemaVersion as a quantity argument.

#### Scenario: High schemaVersion
- **WHEN** a document has a schemaVersion above the maximum
- **THEN** the answer carries code profile.schema-version with errorArgs maxSchemaVersion.

### Requirement: Revision conflict

When a profile publish names a stale newest revision, the server SHALL carry latestRevision as a quantity argument.

#### Scenario: Stale revision
- **WHEN** a publish names a stale revision
- **THEN** the answer carries the NEW code profile.revision-conflict with errorArgs latestRevision.

#### Scenario: Other causes
- **WHEN** a publish fails because the name is in use
- **THEN** the answer carries code profile.conflict with no argument.

### Requirement: Revision not found

When a profile revision read names a missing revision, the server SHALL carry revision as a quantity argument.

#### Scenario: Missing revision
- **WHEN** a read names a missing revision number
- **THEN** the answer carries the NEW code profile.revision-not-found with errorArgs revision.

#### Scenario: Missing profile
- **WHEN** a read names a missing profile
- **THEN** the answer carries code profile.not-found with no argument.
