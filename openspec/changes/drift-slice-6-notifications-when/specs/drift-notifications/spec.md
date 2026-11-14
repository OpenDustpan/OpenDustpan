## ADDED Requirements

### Requirement: Drifted notice

When Record stores a change into drifted, the server SHALL append one unit.drifted notice in the same fenced transaction with scope of the unit namespace, object of the unit reference, objectId of the stable unit ID, address count and console link.

#### Scenario: First drifted from unknown
- **WHEN** Record stores a first drifted result after unknown
- **THEN** one unit.drifted notice is appended with the unit and job dedupe key

#### Scenario: First clean
- **WHEN** Record stores a first in-sync result with no prior
- **THEN** no notice is appended

#### Scenario: Repeat quiet
- **WHEN** Record stores a drifted result while drifted stays set
- **THEN** no notice is appended

#### Scenario: Refused
- **WHEN** Record refuses the result
- **THEN** no notice is written and the store returns the domain refusal

#### Scenario: Failed check
- **WHEN** Record sees a failed check
- **THEN** the last result stays and no notice is appended

#### Scenario: Canceled check
- **WHEN** Record sees a canceled check
- **THEN** the last result stays and no notice is appended

#### Scenario: Stale fence
- **WHEN** Record carries a stale fence
- **THEN** the transaction rolls back with no result and no notice

#### Scenario: Concurrent second Record
- **WHEN** two Records for one unit race on the row lock
- **THEN** at most one notice is committed for the one transition

### Requirement: Cleared notice

When Record stores a change from drifted to in-sync, the server SHALL append one unit.drift-cleared notice in the same fenced transaction with scope of the unit namespace, object of the unit reference and objectId of the stable unit ID.

#### Scenario: Clean after drifted
- **WHEN** Record stores in-sync with prior drifted
- **THEN** one unit.drift-cleared notice is appended with the unit and clearing job dedupe key

#### Scenario: Clean after unknown
- **WHEN** Record stores in-sync with prior unknown
- **THEN** no cleared notice is appended

#### Scenario: Drifted to unknown to drifted
- **WHEN** a state version write sets unknown after drifted and the next check finds drifted
- **THEN** one unit.drifted notice is appended

#### Scenario: Drifted to unknown to in-sync
- **WHEN** a state version write sets unknown after drifted and the next check is clean
- **THEN** one unit.drift-cleared notice is appended

#### Scenario: Retry of clearing job
- **WHEN** the clearing Record retries with the same unit and job dedupe key
- **THEN** the copy is skipped with no second notice
