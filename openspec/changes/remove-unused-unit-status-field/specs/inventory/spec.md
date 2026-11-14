## ADDED Requirements

### Requirement: No status field

The server SHALL omit the status field from each inventory entry and change.

#### Scenario: listUnits omits status
- **WHEN** a principal calls listUnits for a namespace
- **THEN** each entry carries the states array and no status field

#### Scenario: getUnit omits status
- **WHEN** a principal calls getUnit for one unit
- **THEN** the entry carries the states array and no status field

#### Scenario: History and change log omit status
- **WHEN** a principal reads the history view or the inventory change log
- **THEN** each change carries no status field

#### Scenario: Stale status input ignored
- **WHEN** a client sends a status key in a unit write or filter
- **THEN** the stored entry keeps states only and no status value
