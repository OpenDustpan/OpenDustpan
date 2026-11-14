## ADDED Requirements

### Requirement: Guards follow record

When the release record lists a migration file, the server build SHALL run the five shipped-migration checks with no skip.

#### Scenario: Empty record
- **WHEN** the release record lists no file
- **THEN** the five checks skip with the reason

#### Scenario: Listed files
- **WHEN** the release record lists a migration file
- **THEN** the five checks run with no skip

### Requirement: Commented version line

If the release record holds a commented line of version form, then the parser SHALL refuse the record.

#### Scenario: Commented tag
- **WHEN** the record holds a line that starts with #v
- **THEN** Parse fails and each guard fails closed

### Requirement: Tag with no lines

If a version tag has no lines in the release record, then the tag workflow SHALL fail the build.

#### Scenario: Empty tag
- **WHEN** a version tag has no lines in the record
- **THEN** the tag workflow fails before any build

### Requirement: Append-only record

When the record loses a line against the merge base, the tag check SHALL fail the build.

#### Scenario: Removed line
- **WHEN** the record loses a line against the merge base
- **THEN** the check fails

#### Scenario: Missing base
- **WHEN** the merge base is missing
- **THEN** the check fails closed

### Requirement: Listed files intact

When a listed migration file is missing or has a hash other than the listed hash, the migration file check SHALL fail the build.

#### Scenario: Changed hash
- **WHEN** a listed file has another hash
- **THEN** the check fails

#### Scenario: Missing file
- **WHEN** a listed file is missing
- **THEN** the check fails

### Requirement: Round trip after release

When the record says released and CI is set, the PostgreSQL round-trip check SHALL run with no skip.

#### Scenario: CI with no database
- **WHEN** CI is set and the record says released with no database URL
- **THEN** the round-trip test fails
