## ADDED Requirements

### Requirement: Data source claim is strict

When a runner job registers a claim whose saved plan reads a data source, the server SHALL mark the claim strict with the case data-source.

#### Scenario: Nested read
- **WHEN** a saved plan reads a data source in a nested module
- **THEN** the claim is strict with the case data-source

#### Scenario: Bad plan
- **WHEN** the saved plan is missing or malformed
- **THEN** the claim is strict

### Requirement: Strict apply waits

While another claim applies on the same workspace, the server SHALL hold the strict apply until that apply ends.

#### Scenario: Order
- **WHEN** the apply of A ends
- **THEN** the apply of D starts after that end time

### Requirement: History names case

When a strict apply publishes a state version, the server SHALL record the strict case in the history.

#### Scenario: Case in history
- **WHEN** D publishes its state version
- **THEN** the history item names data-source

### Requirement: Direct write refused

While the whole-unit lock is held for a strict apply, the server SHALL answer 423 to a direct state write.

#### Scenario: Direct write
- **WHEN** a direct write arrives during the hold
- **THEN** the answer is 423 with the holder and the case
