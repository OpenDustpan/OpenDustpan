## ADDED Requirements

### Requirement: Format

When the launcher streams a sweep report, the server SHALL refuse a header that is not version 1 of the report format.

#### Scenario: Bad version
- **WHEN** the header version is 2
- **THEN** the server answers 400 with unmanaged.bad-report and stores nothing

### Requirement: Bounds

When the launcher streams a sweep report above a bound, the server SHALL answer 413 and delete the staged blob.

#### Scenario: Long line
- **WHEN** a line has 64 KiB plus one byte
- **THEN** the server answers 413 with unmanaged.report-too-large

### Requirement: Subset

When a record or a header scope is outside the target scopes, the server SHALL refuse the report.

#### Scenario: Foreign scope
- **WHEN** a header scope is not in the target
- **THEN** the server answers 400 with unmanaged.scope-not-in-target

### Requirement: Hash

If the hash of the body differs from the signed header, then the server SHALL fail the upload and store no findings.

#### Scenario: Mismatch
- **WHEN** the body hash differs
- **THEN** the server stores no findings and deletes the staged blob

### Requirement: Record findings

When the report is valid, the server SHALL call RecordFindings in one transaction with the encrypted blob reference.

#### Scenario: Valid report
- **WHEN** a valid report arrives
- **THEN** RecordFindings stores one finding for each resource key and the blob is encrypted
