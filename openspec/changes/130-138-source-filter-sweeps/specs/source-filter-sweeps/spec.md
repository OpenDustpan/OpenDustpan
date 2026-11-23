## ADDED Requirements

### Requirement: Job source check

When a job names a code source outside the stored source filter of its namespace, the server SHALL refuse the create with the error code source.filter-invalid.

#### Scenario: Blocked source
- **WHEN** a job names a source outside the filter and CheckSource runs on the stored filter
- **THEN** the server answers 403 source.filter-invalid and writes an audit entry

#### Scenario: Allowed source
- **WHEN** a job names a source inside the filter and CheckSource runs on the stored filter
- **THEN** CheckSource returns nil and the server creates the job

### Requirement: Source check at claim

When a runner claims a job whose source is now outside the stored source filter, the server SHALL refuse the claim with the error code source.filter-invalid.

#### Scenario: Narrowed filter
- **WHEN** the filter is narrowed after the create
- **THEN** the claim is refused with source.filter-invalid

### Requirement: Sweep pool rule

If a sweep target names a pool that is not a pool of the namespace with the sweep purpose, then the server SHALL refuse the sweep with the error code runner.invalid.

#### Scenario: Pool of another namespace
- **WHEN** the target names a pool of another namespace
- **THEN** the server answers 409 runner.invalid

#### Scenario: Pool of the namespace
- **WHEN** the target names a pool of the namespace with the sweep purpose
- **THEN** the server accepts the target

#### Scenario: Wrong purpose
- **WHEN** the target names a pool of the namespace with the jobs purpose
- **THEN** the server answers 409 runner.invalid

### Requirement: Sweep claim dispatch

When a runner claims a queued sweep, the server SHALL return the assignment only when the pool and the start action allow it.

#### Scenario: Claim without action
- **WHEN** the start of the sweep did not carry namespace:start-sweep and the runner calls ClaimSweep
- **THEN** the claim is refused and an audit entry is written

#### Scenario: Busy target
- **WHEN** a second sweep of the same target starts while the first has not ended
- **THEN** the store refuses it with ErrScanBusy

### Requirement: Report header subset

When a runner streams a sweep report, the server SHALL refuse a header that is not a subset of the target with the error code runner.invalid.

#### Scenario: Extra scope
- **WHEN** the header lists a scope outside the target
- **THEN** the server answers 422 runner.invalid and the sweep ends failed

#### Scenario: Extra region
- **WHEN** the header lists a region outside the regions of the target
- **THEN** the server answers 422 runner.invalid

#### Scenario: Wrong format
- **WHEN** the format string is not opendustpan-sweep-report
- **THEN** the server answers 422 runner.invalid

#### Scenario: Unknown version
- **WHEN** the version is 2
- **THEN** the server answers 422 runner.invalid and writes an audit entry

### Requirement: Report size bounds

While it reads a sweep report, the server SHALL refuse a report over 1 GiB or over 5,000,000 records with the error code runner.limit.

#### Scenario: Too many records
- **WHEN** the 5,000,001st record arrives
- **THEN** the stream stops with 413 runner.limit and the sweep ends failed

#### Scenario: Too large
- **WHEN** the report passes 1 GiB
- **THEN** the stream stops with 413 runner.limit

### Requirement: Report line and tag bounds

While it reads a sweep report, the server SHALL refuse a line over 64 KiB, a record over 50 tags, a tag key over 128 bytes, a tag value over 256 bytes or a header over 1,000 scopes with the error code runner.limit.

#### Scenario: Long line
- **WHEN** a line is 65 KiB
- **THEN** the stream stops with runner.limit

#### Scenario: Tag key
- **WHEN** a tag key is 129 bytes
- **THEN** the stream stops with runner.limit

#### Scenario: Tag value
- **WHEN** a tag value is 257 bytes
- **THEN** the stream stops with runner.limit

#### Scenario: Tag count
- **WHEN** a record has 51 tags
- **THEN** the stream stops with runner.limit

#### Scenario: Scope count
- **WHEN** the header has 1,001 scopes
- **THEN** the stream stops with runner.limit

### Requirement: Failed sweep closes nothing

If a report is truncated or breaks a rule, then the server SHALL end the sweep as failed and close no finding.

#### Scenario: Cut stream
- **WHEN** the stream ends before the last line
- **THEN** the sweep is failed, it never reaches matching and no finding closes
