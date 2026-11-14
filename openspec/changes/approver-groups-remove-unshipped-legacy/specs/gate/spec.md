## ADDED Requirements

### Requirement: Paired approver group IDs

When a gate write holds approver groups, the server SHALL refuse the stored shape unless each group name has its paired group ID.

#### Scenario: Unpaired stored shape refused
- **WHEN** a gate write holds group names without paired group IDs
- **THEN** the server refuses the write with a validation error

#### Scenario: Legacy stored shape read closed
- **WHEN** a stored rule holds group names without paired group IDs
- **THEN** the server fails the whole gate read with a validation error

#### Scenario: Legacy rule stays writable
- **WHEN** a stored rule holds the legacy unpaired shape and a PUT rewrites it
- **THEN** the server accepts a rewrite with paired group IDs

### Requirement: Validate input before ID fill

When a Validate input holds nil group IDs before checkApprovers fills them, the server SHALL accept the input for ID fill.

#### Scenario: Nil IDs before fill allowed
- **WHEN** a Validate input holds nil group IDs before checkApprovers fills them
- **THEN** the server accepts the input for ID fill

### Requirement: Write checks group existence

When a gate write saves approver groups, the server SHALL refuse a group ID that has no group.

#### Scenario: Unknown group ID refused
- **WHEN** a gate write names a group ID that has no group
- **THEN** the server refuses the write with a validation error

#### Scenario: Save counts members
- **WHEN** a gate write saves approver groups
- **THEN** the server counts members during the save check

### Requirement: Renamed group shows current name

When a stored approver group name differs from the current name of the stored group ID, the server SHALL show the current name in reads.

#### Scenario: Stored name differs
- **WHEN** a stored approver group name differs from the current name of the stored group ID
- **THEN** the read shows the current name

### Requirement: Deleted group marked and refused

When an approval rule names a deleted group ID, the server SHALL mark the group as deleted in reads and refuse the job start.

#### Scenario: Read marks deletion by ID
- **WHEN** an approver group ID has no group
- **THEN** the read shows the stored name with the NEW field deleted set true

#### Scenario: Job start refuses deleted group
- **WHEN** an approval rule names a deleted group
- **THEN** the server refuses the job start with the gate ID and the PUT gate route

#### Scenario: Recreated same-name group stays refused
- **WHEN** a group was deleted and recreated under the same name with a new ID
- **THEN** the read keeps the deleted mark and the server refuses the job start without matching by name

### Requirement: Name-only approver reads

When a gate read shows approver groups, the server SHALL show names through a name-only lookup without paging group members.

#### Scenario: Read shows names without paging members
- **WHEN** a gate read shows approver groups
- **THEN** the read shows names without paging group members

#### Scenario: Transient error fails read closed
- **WHEN** the name-only lookup returns a directory error other than not-found
- **THEN** the server fails the whole gate read

### Requirement: Job start rechecks groups

When a job starts on a gate with approver groups, the server SHALL recheck each group ID in the directory at start time and refuse the start on a missing group or any directory error.

#### Scenario: Race between read and start refused
- **WHEN** a group is deleted between the gate read and the job start
- **THEN** the server refuses the job start

#### Scenario: Transient error at start fails closed
- **WHEN** the directory returns a transient error during the job start recheck
- **THEN** the server refuses the job start
