## ADDED Requirements

### Requirement: missing-unit-sentinel

When the unit is gone, the server SHALL fail job.Store.Apply with job.ErrUnitGone.

#### Scenario: Apply for gone unit
- **WHEN** Apply runs for a unit with no inventory row.
- **THEN** Apply returns an error where errors.Is(err, job.ErrUnitGone) is true and stores no job.

#### Scenario: Missing-unit on memory store
- **WHEN** Apply runs on the memory store for a unit with no inventory row.
- **THEN** Apply returns an error where errors.Is(err, job.ErrUnitGone) is true and stores no job.

#### Scenario: Missing-unit on SQLite store
- **WHEN** Apply runs on the SQLite store for a unit with no inventory row.
- **THEN** Apply returns an error where errors.Is(err, job.ErrUnitGone) is true and stores no job.

#### Scenario: Missing-unit on PostgreSQL store
- **WHEN** Apply runs on the PostgreSQL store for a unit with no inventory row.
- **THEN** Apply returns an error where errors.Is(err, job.ErrUnitGone) is true and stores no job.

#### Scenario: Same name made again
- **WHEN** A unit is removed and made again with the same tool name.
- **THEN** Apply succeeds for the new stable ID.

### Requirement: no-driver-error

When jobIndexError handles an unknown violation, the server SHALL return a domain error that does not wrap a driver error.

#### Scenario: Unknown index error hides driver on SQLite
- **WHEN** jobIndexError handles an unknown index violation on SQLite.
- **THEN** errors.As for the SQLite driver error type is false and the message keeps the driver text.

#### Scenario: Unknown index error hides driver on PostgreSQL
- **WHEN** jobIndexError handles an unknown index violation on PostgreSQL.
- **THEN** errors.As for the PostgreSQL driver error type is false and the message keeps the driver text.

#### Scenario: Driver text stays diagnosable
- **WHEN** jobIndexError handles an unknown violation.
- **THEN** The message keeps the driver text for diagnosis.

### Requirement: concurrent-create

When 16 jobs are created at the same time in one namespace, the server SHALL keep 16 jobs with 16 unique seq values.

#### Scenario: Sixteen parallel creates
- **WHEN** 16 goroutines each create one job in one namespace.
- **THEN** Paging back all jobs returns 16 jobs with 16 unique seq values.

#### Scenario: Seq collision retries
- **WHEN** Two concurrent creates collide on seq.
- **THEN** The store retries inside the boundary and returns one stored job with a unique seq without a duplicate-key driver error.

#### Scenario: Unit removed during concurrent create
- **WHEN** A unit is removed while concurrent creates run in the same namespace.
- **THEN** Creates for the removed unit fail with job.ErrUnitGone and creates for live units keep all jobs with unique seq values.
