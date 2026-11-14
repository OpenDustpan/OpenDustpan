## ADDED Requirements

### Requirement: Static binary

The launcher SHALL ship as one static binary with no cgo for linux amd64 and linux arm64.

#### Scenario: static build
- **WHEN** the release builds for linux amd64 and linux arm64
- **THEN** the static check passes

### Requirement: No store or forge links

The launcher SHALL link no SQL driver and no forge client.

#### Scenario: dependency check
- **WHEN** the binary links a SQL driver or a forge client
- **THEN** the check fails the build

### Requirement: Strict flags

When a flag is unknown, repeated or without a value, the launcher SHALL end with exit code 64.

#### Scenario: bad flag
- **WHEN** a flag is repeated
- **THEN** the launcher ends with exit code 64

### Requirement: Handoff failure

When no handoff token exists or the exchange fails, the launcher SHALL end with exit code 65.

#### Scenario: no token
- **WHEN** no handoff token exists
- **THEN** the launcher starts no step

### Requirement: Secrets directory

If the secrets directory is not on tmpfs or ramfs, then the launcher SHALL refuse the run before any step starts.

#### Scenario: wrong backing
- **WHEN** the secrets directory is on disk
- **THEN** the launcher refuses

### Requirement: Masking

The launcher SHALL mask each session secret of 4 or more bytes with [masked] in every step log.

#### Scenario: split secret
- **WHEN** a secret crosses a chunk end
- **THEN** the log holds [masked] only

### Requirement: Copy mode

When the operator runs copy with --dest, the launcher SHALL copy its binary to that directory.

#### Scenario: copy
- **WHEN** the destination is writable
- **THEN** the binary is in the directory
