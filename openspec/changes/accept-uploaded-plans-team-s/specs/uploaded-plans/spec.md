## ADDED Requirements

### Requirement: Upload a plan

When a caller with the plan grant uploads both plan parts for one unit, the server SHALL store them as one unit plan of a new change order marked as uploaded plan.

#### Scenario: Upload makes a marked change order
- **WHEN** a caller with the plan grant uploads plan-display and plan-apply parts for one unit
- **THEN** the server stores both parts, marks the change order as uploaded plan, and binds the unit plan to both part hashes

#### Scenario: Missing part is refused
- **WHEN** an upload omits the plan-display part or the plan-apply part
- **THEN** the server refuses the upload without a stored plan

#### Scenario: Second upload supersedes
- **WHEN** a second upload arrives while the first unit plan waits in review
- **THEN** the server moves the revision, keeps the uploaded mark, and voids prior approvals

### Requirement: Upload integrity

When an uploaded part fails its size or hash check, the server SHALL refuse the upload without a stored plan.

#### Scenario: Hash mismatch is refused
- **WHEN** the claimed hash of an uploaded part differs from the SHA-256 of its bytes
- **THEN** the server refuses the upload without a stored plan

#### Scenario: Oversize part is refused
- **WHEN** an uploaded part is larger than 256 MiB
- **THEN** the server refuses the upload without a stored plan

### Requirement: Bind apply to hash

When a unit with an approved uploaded plan receives a state write or update start, the server SHALL refuse the write unless both presented hashes equal the approved stored plan hashes.

#### Scenario: Same hashes pass
- **WHEN** the state write or update start presents both hashes equal to the approved stored plan
- **THEN** the write passes the hash check and continues to gates and approvals

#### Scenario: Different hash is refused
- **WHEN** the state write or update start presents hashes different from the approved stored plan
- **THEN** the server refuses the write and names the change order, the stored plan ID, and the expected hashes

#### Scenario: Missing hash is refused
- **WHEN** the state write or update start presents no plan hash for a unit with an approved uploaded plan
- **THEN** the server refuses the write and names the change order

#### Scenario: Replayed hash is refused
- **WHEN** the state write presents the hashes of a superseded upload after a newer upload
- **THEN** the server refuses the write and names the current change order revision

#### Scenario: Write before supersede passes
- **WHEN** the state write lands before a concurrent second upload commits
- **THEN** the server checks the write against the prior revision and does not name the newer revision

### Requirement: No uploaded plan skips check

When a unit holds no approved uploaded plan, the server SHALL skip the uploaded hash check for its state write or update start.

#### Scenario: Write without approved upload proceeds
- **WHEN** a state write or update start arrives for a unit with no approved uploaded plan
- **THEN** the server skips the uploaded hash check and applies the normal fixed controls

### Requirement: Show weaker mark

When a reader opens a change order from an upload, the server SHALL show the uploaded-plan mark with the bound hashes.

#### Scenario: Review shows the mark
- **WHEN** a reader with change order read opens the change order
- **THEN** the change order and each unit plan show the uploaded-plan mark and the bound hashes

#### Scenario: Reader without grant is refused
- **WHEN** a reader without change order read opens the uploaded change order
- **THEN** the server refuses the read without plan content

#### Scenario: Reader without plan grant sees masked hashes
- **WHEN** a reader holds change order read but not plan read on the unit
- **THEN** the server masks the hash values and shows the uploaded-plan mark only
