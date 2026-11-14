## ADDED Requirements

### Requirement: Mark unknown

When another job writes a state version, the server SHALL set the drift state to unknown in that same fenced transaction.

#### Scenario: State write
- **WHEN** another job writes a state version
- **THEN** drift state is unknown in the same transaction

#### Scenario: Stale fence
- **WHEN** MarkUnknown carries a stale fence
- **THEN** the state write rolls back and the drift state is unchanged

#### Scenario: Race with Record
- **WHEN** a state version write races a Record for one unit
- **THEN** the row lock orders them and the last committed write wins with at most one notice
