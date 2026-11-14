## ADDED Requirements

### Requirement: Override forbidden

If a wider level forbids overrides, then the server SHALL refuse a narrower value that differs from it with status 409 that names that level.

#### Scenario: Namespace forbids
- **WHEN** a unit sets pre-merge while the namespace forbids overrides
- **THEN** the write is refused with 409 and names the namespace setting

#### Scenario: Collection forbids
- **WHEN** the namespace allows overrides, the collection forbids them and a unit sets a different value
- **THEN** the write is refused with 409 and names the collection setting

### Requirement: Unit value wins

Where every wider level allows overrides, the server SHALL use the unit value as the effective timing.

#### Scenario: Unit value wins
- **WHEN** the namespace and the collection allow overrides and the unit sets pre-merge
- **THEN** the effective value is pre-merge and names the unit level

### Requirement: Inherit

If a unit has no value, then the server SHALL use the value of the narrowest wider level that sets one.

#### Scenario: Unit unset
- **WHEN** the unit has no value and the collection sets pre-merge
- **THEN** the effective value is pre-merge and names the collection level

### Requirement: Timing audit

When a person writes an apply timing value, the server SHALL write an audit entry with the level, the old value, the new value and the principal.

#### Scenario: Write audited
- **WHEN** a namespace administrator sets the namespace value
- **THEN** one audit entry holds the level, both values and the principal
