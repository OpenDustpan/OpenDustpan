## ADDED Requirements

### Requirement: The server keeps a ring of session keys

The server SHALL keep each session key version random, wrapped by the key-encryption key, in SQL, and SHALL refuse to start when a stored version does not unwrap. A new version SHALL open at once and seal after its `SealsFrom`. The ring SHALL rotate each `session.keyRotationDays` days, at the seal count of a replica, and on `retireAll`. Each change SHALL write an audit entry.

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

### Requirement: A superseded version retires

A version that the next version superseded SHALL retire after the longest maximum length that the session policy allows plus one reload interval, and the server SHALL stop opening it at that time in memory, whatever the result of the write that records the retirement, and SHALL retry a failed write. The server SHALL write `session:key-retired` when the write succeeds.

#### Scenario: Superseded version retires
- **WHEN** a version was superseded for longer than the longest maximum length plus one reload interval
- **THEN** the server retires it, writes `session:key-retired`, and a cookie that names it fails to open

### Requirement: Key material stays out of backups and output

A backup SHALL hold no session key version. No log record, error, audit entry or route body SHALL hold a key, a wrapped key or a part of either.

#### Scenario: Export of a ring
- **WHEN** the server exports a backup of a database with a ring
- **THEN** the archive holds no key ID, wrapped key or wrapper ID of a version

### Requirement: Retire all follows the fresh-step policy

The fresh step of `retireAll` SHALL follow the fresh-step policy of the installation.

#### Scenario: Retire all with the fresh-step setting off
- **WHEN** the installation does not require a fresh step and an administrator with the action and a reason rotates with `retireAll`
- **THEN** the rotation succeeds with no fresh step