## ADDED Requirements

### Requirement: Upload a plan

When a caller with the plan grant uploads both plan parts for one unit, the server SHALL store them as one unit plan of a new change order marked as uploaded plan.

#### Scenario: Upload makes a marked change order
- **WHEN** a caller with the plan grant uploads both parts
- **THEN** the server stores both parts and marks the change order

### Requirement: Bind apply to hash

When a unit with an approved uploaded plan receives a state write or update start, the server SHALL refuse the write unless both presented hashes equal the approved hashes.

#### Scenario: Different hash is refused
- **WHEN** the write presents other hashes
- **THEN** the server refuses the write and names the change order

#### Scenario: Same hashes pass
- **WHEN** the write presents the approved hashes
- **THEN** the write passes the hash check
