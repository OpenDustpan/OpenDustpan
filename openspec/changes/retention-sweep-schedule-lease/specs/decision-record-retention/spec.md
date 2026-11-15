## ADDED Requirements

### Requirement: Scheduled sweep

While the worker component runs, the server SHALL prune expired decision records on the configured schedule.

#### Scenario: Expired record
- **WHEN** a decision record is older than the retention value and the schedule fires
- **THEN** the sweep deletes the record and writes an audit record with the count

### Requirement: One sweep at a time

If another server process holds the sweep lease, then the server SHALL skip the sweep.

#### Scenario: Two processes
- **WHEN** two server processes fire the schedule together
- **THEN** one process prunes and the other logs that it skipped
