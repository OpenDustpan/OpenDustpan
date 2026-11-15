## ADDED Requirements

### Requirement: Resolve start inputs from settings

When a job starts, the server SHALL resolve pool, code location, profile reference and steps from the request override, then the unit settings, then the namespace unit defaults.

#### Scenario: Trigger start with stored location
- **WHEN** a schedule trigger fires and the unit settings hold a location
- **THEN** the server starts a plan job with the stored inputs

#### Scenario: Missing location
- **WHEN** a start has no location in the request, settings or defaults
- **THEN** the server answers 422 and creates no job

### Requirement: Run-as principal read check

When a job with a trigger reads the outputs of another unit, the server SHALL allow the read only when the sharing rule names the job unit and trigger.OutputReadCheck.Allow allows the run-as principal and the manual-fire caller.

#### Scenario: Run-as principal loses access
- **WHEN** the run-as principal of the trigger loses unit:read-outputs on the source after the job starts
- **THEN** the next read answers 403 with no secret value
