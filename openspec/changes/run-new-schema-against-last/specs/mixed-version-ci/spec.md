## ADDED Requirements

### Requirement: Tag matrix

When the harness starts the new binary and each of the last 2 ancestor v* tags on one database, the server SHALL complete tool steps against each old binary.

#### Scenario: Two old binaries beside the new binary
- **WHEN** the harness starts the new binary on a fresh database and then starts each of the last 2 ancestor v* tags
- **THEN** real pulumi up and tofu apply steps against each old binary complete while the new binary serves on the same database

### Requirement: Tag choice

When 3 or more tags exist with extra non-ancestor or non-v* tags, the server SHALL run with the newest 2 ancestor v* tags.

#### Scenario: Newest two ancestors
- **WHEN** 3 or more tags exist with extra non-ancestor or non-v* tags
- **THEN** the newest 2 ancestor v* tags run and non-ancestor tags and non-v* tags never run

### Requirement: Single tag

When exactly 1 ancestor v* tag exists beside the new binary on one database, the server SHALL complete tool steps against that old binary.

#### Scenario: One old binary beside the new binary
- **WHEN** exactly 1 ancestor v* tag exists
- **THEN** tool steps against the one old binary complete while the new binary serves on the same database

### Requirement: No tags before first tag

When no ancestor v* tag exists on a full clone, the server SHALL report zero old binaries and complete the new-binary tool steps.

#### Scenario: Explicit no-old-binary report
- **WHEN** no ancestor v* tag exists on a full clone
- **THEN** the run reports zero old binaries and runs the new-binary tool steps

### Requirement: Shallow clone

When the clone is shallow or tags were not fetched, the server SHALL fail the run and name the missing tag fetch.

#### Scenario: Loud failure on missing tags
- **WHEN** the clone is shallow or tags were not fetched
- **THEN** the harness fails and names the missing tag fetch instead of reporting zero old binaries

### Requirement: Build and start failure

When one old tag binary fails to build, migrate, or start, the server SHALL fail the run and name the failed tag.

#### Scenario: One old tag fails
- **WHEN** one old tag binary fails to build, migrate, or start
- **THEN** the run fails and names the failed tag even when the other old tag works

### Requirement: Pre-minimum tag

When an old tag binary predates the minimum-check code, the server SHALL fail the run and name the tag.

#### Scenario: Old tag without refusal code
- **WHEN** an old tag binary predates the minimum-check code
- **THEN** the run fails and names the tag

### Requirement: SQLite sequential

When the harness runs the matrix on SQLite, the server SHALL serve one binary at a time on the same file.

#### Scenario: One SQLite server at a time
- **WHEN** the harness runs the matrix on SQLite
- **THEN** each binary serves alone on the same SQLite file and tool steps complete with no concurrent servers

### Requirement: Start order

When the old binary starts before the new binary on the same PostgreSQL database, the server SHALL complete tool steps against the old binary.

#### Scenario: Reverse start order
- **WHEN** the old binary starts first and the new binary starts second on the same PostgreSQL database
- **THEN** tool steps against the old binary still complete

### Requirement: Dual dialects

When DUSTPAN_TEST_POSTGRES_URL is set, the server SHALL pass the tag matrix and the self-check on SQLite and on PostgreSQL.

#### Scenario: Matrix on both dialects
- **WHEN** the harness runs with DUSTPAN_TEST_POSTGRES_URL set
- **THEN** the tag matrix and the self-check pass on SQLite and on PostgreSQL

### Requirement: Below-minimum self-check

When the self-check starts an old binary on a scratch database with a raised test minimum, the server SHALL refuse the old binary writes with error code schema.binary-below-minimum.

#### Scenario: Stale holder refused
- **WHEN** the self-check starts an old binary on a scratch database with a raised test minimum
- **THEN** the old binary refuses its own writes with error code schema.binary-below-minimum
