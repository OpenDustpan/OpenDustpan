## ADDED Requirements

### Requirement: StartJob

When StartJob starts a job, the server SHALL store the effective pool ID and the effective code location of the job.

#### Scenario: StartJob applies defaults
- **WHEN** StartJob starts a job with no pool and no code location
- **THEN** the stored job carries the unit pool ID and the unit code location as its effective values

#### Scenario: StartJob keeps explicit values
- **WHEN** StartJob starts a job that names its own pool and code location
- **THEN** the stored job carries the requested pool ID and code location

#### Scenario: StartJob refuses without default
- **WHEN** StartJob starts a job with no pool or code location and the unit has no default
- **THEN** StartJob refuses with the NEW error code `job.defaults-missing` and names the missing field without repeating input

### Requirement: ShowEffectiveJobValues

When a caller reads one job through ShowEffectiveJobValues, the server SHALL return the stored effective pool ID and the stored effective code location of the job.

#### Scenario: ShowEffectiveJobValues reads stored snapshot
- **WHEN** a caller with read permission reads one job through ShowEffectiveJobValues
- **THEN** the answer includes the stored effective pool ID and the stored effective code location labeled as values at start

#### Scenario: ShowEffectiveJobValues reads pre-change job
- **WHEN** a caller reads a job stored before this change
- **THEN** the answer returns empty effective values with present set to false

#### Scenario: ShowEffectiveJobValues after pool rename
- **WHEN** the pool was renamed or removed after StartJob
- **THEN** the answer returns the stored stable pool ID with the stored name snapshot

### Requirement: RecordAutomaticAction

When RecordAutomaticAction records a unit default applied to a job, the server SHALL write an audit entry that names the server actor and the rule reason.

#### Scenario: RecordAutomaticAction writes default entry
- **WHEN** RecordAutomaticAction records a unit default applied to a job
- **THEN** the entry names server actor `server` and reason rule `unit-default-applied` readable at `GET /api/audit/entries`

#### Scenario: RecordAutomaticAction read denied
- **WHEN** a caller without grant reads `GET /api/audit/entries`
- **THEN** the server denies with code `audit.entry-read-unavailable` or a grant refusal without leaking entry content
