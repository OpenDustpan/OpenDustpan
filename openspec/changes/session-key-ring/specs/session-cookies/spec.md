## ADDED Requirements

### Requirement: The server keeps a ring of session keys

The server SHALL keep each session key version random, wrapped by the key-encryption key, in SQL, and SHALL refuse to start when a stored version does not unwrap. A new version SHALL open at once and seal after its `SealsFrom`. The ring SHALL rotate each `session.keyRotationDays` days, at the seal count of a replica, and on `retireAll`. Each change SHALL write an audit entry. A backup SHALL hold no version.

#### Scenario: Start with no ring
- **WHEN** the server starts and no version exists
- **THEN** it makes the first version and writes an audit entry

#### Scenario: Version that does not unwrap
- **WHEN** a stored version does not unwrap
- **THEN** the server stops at the start and the error names the key ID and the wrapper

#### Scenario: Retire all
- **WHEN** an administrator with the action, a reason and a fresh step rotates with `retireAll`
- **THEN** each earlier version retires and each cookie that names one fails to open within one reload interval

### Requirement: A sealed value opens only as its kind

The sealer SHALL seal with AES-256-GCM and a random 96-bit nonce, and the associated data SHALL name the format version, the kind and the key ID. A value of one kind SHALL NOT open as another. A value over 4096 bytes SHALL be refused before the open.

#### Scenario: Value of another kind
- **WHEN** a refresh cookie value is given as an access cookie
- **THEN** the open fails with `ErrCookieOpen`
