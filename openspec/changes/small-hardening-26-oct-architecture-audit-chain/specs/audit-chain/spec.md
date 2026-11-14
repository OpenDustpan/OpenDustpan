## ADDED Requirements

### Requirement: Record filter change

When validation refuses an old filter value, the server SHALL call Record filter change to store its SHA-256 hex and byte length in Change.Old and store the first 256 bytes only when the value length is 256 bytes or less.

#### Scenario: oversize old value via Record filter change
- **WHEN** old filter value exceeds 256 bytes and validation refuses it
- **THEN** Change.Old holds SHA-256 hex and byte length and holds no value bytes

#### Scenario: exact 256 byte old value via Record filter change
- **WHEN** old filter value of exactly 256 bytes fails validation
- **THEN** Change.Old holds SHA-256 hex, byte length 256, and the kept 256 bytes

#### Scenario: small old value via Record filter change
- **WHEN** old filter value of less than 256 bytes fails validation
- **THEN** Change.Old holds SHA-256 hex, byte length, and kept bytes

#### Scenario: non-UTF8 old value via Record filter change
- **WHEN** old filter value holds bytes that are not valid UTF-8
- **THEN** Change.Old holds SHA-256 hex and byte length and holds no value bytes
