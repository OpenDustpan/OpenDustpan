## ADDED Requirements

### Requirement: delegated-step-is-audited

When an agent acts under a delegation, the server SHALL write one audit entry whose agent block holds the delegation ID, and SHALL write the reason in the `Reason` field of the entry.

#### Scenario: tool-call-under-delegation
- **WHEN** an agent calls a tool under a live delegation with a valid reason
- **THEN** one entry exists with the agent block that holds the delegation ID, and with the reason

### Requirement: denied-delegated-call-is-audited

When a delegated call is refused because it is outside the scope or because the delegation ended, the server SHALL write one denial entry that names the delegation where known and the cause.

#### Scenario: call-outside-the-scope
- **WHEN** an agent calls outside the scope of its delegation, or under an ended delegation
- **THEN** one denial entry exists that names the delegation where known and the cause

### Requirement: invalid-reason-is-refused

When a delegated call carries a reason that `audit.ValidateReason` refuses, the server SHALL refuse the call with `audit.reason-invalid`, SHALL repeat nothing of the input and SHALL write no entry.

#### Scenario: invalid-reason
- **WHEN** a delegated call carries an invalid reason
- **THEN** the server answers 400 with `audit.reason-invalid` and writes no entry

### Requirement: failed-append-refuses-the-step

When the audit append of a delegated step fails, the server SHALL refuse the step with no effect.

#### Scenario: append-fails
- **WHEN** the append of a delegated step fails
- **THEN** the step has no effect and no step entry exists

### Requirement: read-delegated-entries

When an authorized caller lists audit entries, the server SHALL return the agent block and the reason of each delegated entry, and SHALL refuse a read of a scope that the caller may not read.

#### Scenario: list-entries
- **WHEN** an authorized caller lists the entries
- **THEN** each delegated entry returns its agent block and its reason

#### Scenario: cross-namespace-read
- **WHEN** a caller with a token of one namespace reads the entries of another
- **THEN** the server answers 404 with `audit.entry-not-found`

### Requirement: chain-covers-the-delegation

The chain check SHALL include the delegation ID and the reason of each delegated entry, with no block version and no path for old entries.

#### Scenario: chain-passes
- **WHEN** a caller checks a chain that holds delegated entries
- **THEN** the check passes

#### Scenario: tampered-entry
- **WHEN** the delegation ID or the reason of a stored entry was changed or removed
- **THEN** the check fails and names the first bad sequence number

### Requirement: reads-fail-fast-in-a-database-gap

When a caller lists entries or checks the chain while the database is unavailable, the server SHALL answer 503 with `Retry-After: 5` and `audit.entry-read-unavailable`.

#### Scenario: read-during-a-gap
- **WHEN** the database is unavailable
- **THEN** the server answers 503 with `Retry-After: 5` and `audit.entry-read-unavailable`

### Requirement: installation-scope-steps-are-audited

When an agent acts on an object with no namespace, the server SHALL write the entry in the installation scope chain.

#### Scenario: installation-step
- **WHEN** an agent acts on an object with no namespace
- **THEN** one entry exists in the installation scope chain with the block and the reason
