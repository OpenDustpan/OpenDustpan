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

When the poll skips an operation whose job or namespace read failed, the server SHALL increase the counter job_claim_poison_skips_total.

#### Scenario: Poison skip counted
- **WHEN** the poll skips an operation whose job or namespace read failed
- **THEN** the counter job_claim_poison_skips_total increases by one and the log names the job reference

#### Scenario: Poison alert
- **WHEN** one operation is skipped 10 times for failed reads
- **THEN** the server raises an operator alert with the job reference

### Requirement: bound-count

When operations exceed the poll bound of 64, the server SHALL count each skipped operation in the poll-bound skip metric.

#### Scenario: Poll bound reached
- **WHEN** operations exceed the poll bound of 64
- **THEN** each skipped operation is counted in the poll-bound skip metric and logged with its job reference

### Requirement: bound-fairness

When operations were skipped because of the poll bound of 64, the server SHALL process each skipped operation on a later poll in poll order.

#### Scenario: Bound skips processed later
- **WHEN** operations were skipped because of the poll bound of 64
- **THEN** a later poll in poll order processes each skipped operation from its cursor

### Requirement: bind-phase-check

While the bindClaim phase check stays unreachable, the server SHALL keep the check with a defense-in-depth comment.

#### Scenario: Defense comment present
- **WHEN** a reviewer reads bindClaim in pkg/store/internal/jobstore/apply.go
- **THEN** bindClaim carries a comment that states the phase check is defense in depth
