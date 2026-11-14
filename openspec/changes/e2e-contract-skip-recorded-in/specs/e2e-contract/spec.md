## ADDED Requirements

### Requirement: cross-run-skip

When a contract line is missing in a run and the skip for that line was recorded by a different run, the check SHALL report the line as missing for that run.

#### Scenario: cross-run missing
- **WHEN** a line is missing in one run and only another run in the same directory recorded a skip for it
- **THEN** the check reports the line as missing for the first run

#### Scenario: same-run excused
- **WHEN** a line is missing in a run that recorded a skip for it
- **THEN** the check reports the line as not checked

### Requirement: skip-identity

If a skip carries no run identity or names a run that is absent from the report directory, then the check SHALL excuse no contract line for that skip.

#### Scenario: unknown identity
- **WHEN** a skip names no run or an unknown run
- **THEN** the check reports the line as missing
