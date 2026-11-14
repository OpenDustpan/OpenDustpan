## ADDED Requirements

### Requirement: Prune

When the hourly prune runs, the server SHALL delete each used_tickets row with expiry at or before the cutoff through PruneTickets.

#### Scenario: Prune deletes expired rows
- **WHEN** the prune runs on expired and live rows, also after a restore
- **THEN** only expired rows are deleted, and the log record and the metric show the count.

### Requirement: Start-up prune

When the server starts, the server SHALL run the used_tickets prune once before the first hour ends.

#### Scenario: Run at start
- **WHEN** the server starts with expired rows in used_tickets
- **THEN** the first prune deletes them and writes one log record.

### Requirement: Constructor

If the store or the clock is nil, then NewStoreTickets SHALL return an error.

#### Scenario: Nil store refused
- **WHEN** NewStoreTickets gets a nil store
- **THEN** it returns an error and no value.

### Requirement: Prune failure

If the store returns an error during the prune, then the server SHALL delete no row and log the failure.

#### Scenario: Store error logged
- **WHEN** the store fails during the prune
- **THEN** the log record shows the failure and the counter does not change.
