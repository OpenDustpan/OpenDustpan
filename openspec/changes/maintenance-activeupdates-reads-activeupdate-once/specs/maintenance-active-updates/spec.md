## ADDED Requirements

### Requirement: Batch active-update check

The server SHALL read the active updates of a namespace in one batch read per check.

#### Scenario: ListActiveUpdates flat reads across stack counts
- **WHEN** maintenance checks the active updates of a namespace
- **THEN** the number of maintenance store reads is the same for 3 and for 30 stacks

#### Scenario: ListActiveUpdates equivalence with single reads
- **WHEN** a namespace holds running updates and an empty namespace holds none
- **THEN** the batch result equals the set from repeated ActiveUpdate calls with deleted stacks skipped

#### Scenario: ListActiveUpdates batch read fails
- **WHEN** the batch read returns an error
- **THEN** the server refuses the enter with maintenance.unavailable and does not enter maintenance

### Requirement: Restore double check

While a restore checks active updates, the server SHALL use the same batch read for each check.

#### Scenario: ListActiveUpdates restore keeps refusal
- **WHEN** a restore checks active updates twice
- **THEN** the second check also uses the batch read and refuses with maintenance.busy while an update runs
