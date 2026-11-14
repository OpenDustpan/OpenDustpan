## ADDED Requirements

### Requirement: static-binary

The launcher SHALL ship as one static binary with no cgo for linux amd64 and linux arm64.

#### Scenario: static release binary
- **WHEN** the release builds for linux amd64 and linux arm64
- **THEN** the build ends with no cgo and the static check passes

### Requirement: no store or forge links

The launcher SHALL link no SQL driver and no forge client.

#### Scenario: dependency check
- **WHEN** the binary links a SQL driver or a forge client
- **THEN** the check fails the build

### Requirement: strict flags

When a flag is unknown, repeated, or without a value, the launcher SHALL end with exit code 64.

#### Scenario: bad flag
- **WHEN** a flag is unknown, repeated, or without a value
- **THEN** the launcher ends with exit code 64

### Requirement: four modes

The launcher SHALL provide run, copy, OpenTofu credentials helper, and git credential helper modes.

#### Scenario: copy mode
- **WHEN** the operator runs copy with --dest
- **THEN** the launcher copies its binary to the given directory and ends

#### Scenario: credentials helper
- **WHEN** the program name is terraform-credentials-dustpan
- **THEN** the launcher answers the OpenTofu credentials request from its session

#### Scenario: git helper
- **WHEN** the program name is git-credential-dustpan
- **THEN** the launcher answers the git credential request for a tool step

### Requirement: run order

The launcher SHALL follow the run order of prepare place, open session, run steps, and report outcome.

#### Scenario: refused secrets place
- **WHEN** the secrets directory fails the memory-volume check
- **THEN** the launcher refuses before any step starts

#### Scenario: finished phase
- **WHEN** the claimed phase ends and the server holds its outcome
- **THEN** the launcher ends with exit code 0

### Requirement: step execution

The launcher SHALL start each step with no shell in the step directory with null input and piped output.

#### Scenario: step without shell
- **WHEN** a step starts
- **THEN** the launcher starts the bare name or clean absolute path with no shell

### Requirement: timeouts and signals

The launcher SHALL enforce the timeout and signal handling of each step with SIGINT and then SIGKILL after the grace period.

#### Scenario: step timeout
- **WHEN** a step reaches its timeout or cancel arrives
- **THEN** the launcher sends SIGINT to the process group and then SIGKILL after the grace period

#### Scenario: stop of launcher
- **WHEN** the launcher receives SIGTERM as the first process
- **THEN** the launcher stops the step in the same way

### Requirement: secrets directory

The launcher SHALL keep tool-step credentials in a 0700 owned directory on tmpfs or ramfs with 0600 files.

#### Scenario: wrong backing store
- **WHEN** the secrets directory is not on tmpfs or ramfs
- **THEN** the launcher refuses before any step starts

#### Scenario: per-step cleanup
- **WHEN** the tool step ends
- **THEN** the launcher deletes the per-step directory

### Requirement: masking

The launcher SHALL mask each session secret of 4 or more bytes with [masked] in every step log.

#### Scenario: split secret
- **WHEN** a secret crosses a log chunk end
- **THEN** the stored log holds [masked] and no plain secret

#### Scenario: short value
- **WHEN** a value is shorter than 4 bytes
- **THEN** the launcher leaves the value in plain text

### Requirement: handoff failure

When no handoff token exists or the exchange fails, the launcher SHALL end with exit code 65.

#### Scenario: missing or failed exchange
- **WHEN** no handoff token exists or the exchange fails
- **THEN** the launcher ends with exit code 65 and starts no step

### Requirement: helper and copy failures

When the helper has no live session or the copy destination is missing or not writable, the launcher SHALL refuse the request.

#### Scenario: helper without session
- **WHEN** the helper has no live session
- **THEN** the helper ends with a nonzero code and writes no credentials

#### Scenario: bad copy destination
- **WHEN** the destination is missing or not writable
- **THEN** the copy ends with a nonzero code

### Requirement: dumpable and step user

On Linux the launcher SHALL set dumpable to 0 and run each step as its assigned step user where the place allows it.

#### Scenario: launcher start on Linux
- **WHEN** the launcher starts on Linux
- **THEN** the launcher sets dumpable to 0 before any step starts
