## ADDED Requirements

### Requirement: Fill unlocked write facts

When an unlocked write on the OpenTofu HTTP backend carries a new state, the server SHALL fill state.available, state.resources and state.removed from the real current state and the real new state.

#### Scenario: Removed instance matches
- **WHEN** an unlocked write removes web[1]
- **THEN** state.removed holds web[1] and a gate on it matches

#### Scenario: First write
- **WHEN** an unlocked write is the first state of a unit
- **THEN** state.removed is empty and the recheck is skipped

### Requirement: Fail closed on bad state

If the state cannot be read, is above 10 MB or 20000 instances, or holds encrypted_data but is not the OpenTofu envelope, then the server SHALL refuse the unlocked write with a catalog code.

#### Scenario: Over limit
- **WHEN** the new state has 20001 instances
- **THEN** the server refuses the write with state.over-limit and writes an audit entry

### Requirement: Recheck current state

While it commits an unlocked write with filled facts, the server SHALL check that the current state version is the one the facts came from.

#### Scenario: Current changed
- **WHEN** the current state version changes before the commit
- **THEN** the server refuses the write with state.changed and the client retries

### Requirement: Save-time visibility

When a new state.write gate reads state.resources or state.removed without state.available, the server SHALL refuse the save with gate.invalid.

#### Scenario: Missing flag
- **WHEN** such a gate is saved
- **THEN** the server refuses it with gate.invalid and names state.available
