## ADDED Requirements

### Requirement: Record parent and claim

When the server commits a merged state version, the server SHALL store its parent version ID and its claim ID.

#### Scenario: Merged version history
- **WHEN** two disjoint applies merge
- **THEN** history lists two new state versions and each version names its stable claim ID and the stable parent version IDs committed by merge-core

#### Scenario: Single apply history
- **WHEN** one claim applies with no concurrent claim
- **THEN** history lists one new state version with its claim ID and its parent version ID

#### Scenario: Three claim history
- **WHEN** three disjoint claims merge
- **THEN** history lists each new state version with its own claim ID

#### Scenario: Legacy version history
- **WHEN** a caller reads a version made before this change or by a non-claim write
- **THEN** history lists the version with an empty claim field and the label no claim

### Requirement: Atomic record

When the server commits a merge, the server SHALL write the version rows and the merged audit entry in one fenced transaction.

#### Scenario: Restart mid-commit
- **WHEN** the server restarts before the merge transaction commits
- **THEN** the store holds no new version row and no merged audit entry

#### Scenario: Fence loss mid-commit
- **WHEN** the fence check fails before commit
- **THEN** the store holds no new version row and no merged audit entry

#### Scenario: Failover mid-commit
- **WHEN** a database failover interrupts the merge transaction
- **THEN** the caller gets 503 with Retry-After and the store holds no partial version and no partial audit entry

#### Scenario: Audit write failure
- **WHEN** the merged audit write fails after the version row write
- **THEN** the store holds no new version row

#### Scenario: Concurrent merge race
- **WHEN** two merges commit against the same parent at the same time
- **THEN** one merge commits and the other merge retries on the new parent or fails closed with no lost resource

### Requirement: History authorization

While serving workspace history, the server SHALL check unit read action on the workspace.

#### Scenario: Denied history read
- **WHEN** a caller without unit read action requests the state version list
- **THEN** the server denies the read with 403

### Requirement: Keep sealed values

When the server commits a merge, the server SHALL keep each sealed value sealed under the scope of the unit.

#### Scenario: Sealed merge
- **WHEN** a merge combines sealed values from two parents
- **THEN** state reads return masked values for secret values from both parents

#### Scenario: Unsealed merge fails
- **WHEN** merge output holds a secret value in plain text
- **THEN** the server refuses the merge and writes no version
