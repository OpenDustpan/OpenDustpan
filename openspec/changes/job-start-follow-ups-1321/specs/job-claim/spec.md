## ADDED Requirements

### Requirement: poison-skip

When the read of a job or its namespace fails during a poll, the server SHALL skip that operation for that poll.

#### Scenario: Failed job read
- **WHEN** the read of a job or its namespace fails during a poll
- **THEN** the poll skips that operation for that poll, logs the job reference, and continues with the next operation

#### Scenario: Skipped operation retried
- **WHEN** a poll skipped an operation whose job or namespace read failed
- **THEN** the next poll reads the skipped operation again

### Requirement: poison-count

When the poll skips an operation whose job or namespace read failed, the server SHALL increase the poison skip count.

#### Scenario: Poison skip counted
- **WHEN** the poll skips an operation whose job or namespace read failed
- **THEN** the poison skip count increases by one

### Requirement: bound-count

When operations exceed the poll bound of 64, the server SHALL count each skipped operation in the poll-bound skip metric.

#### Scenario: Poll bound reached
- **WHEN** operations exceed the poll bound of 64
- **THEN** each skipped operation is counted in the poll-bound skip metric and logged with its job reference

### Requirement: bound-fairness

When operations were skipped because of the poll bound of 64, the server SHALL process each skipped operation on a later poll.

#### Scenario: Bound skips processed later
- **WHEN** operations were skipped because of the poll bound of 64
- **THEN** a later poll processes each skipped operation with no starvation

### Requirement: bind-phase-check

When the claim phase does not match the job phase, the server SHALL refuse the bind.

#### Scenario: Phase mismatch refused
- **WHEN** the claim phase does not match the job phase
- **THEN** the server refuses the bind
