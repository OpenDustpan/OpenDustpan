## ADDED Requirements

### Requirement: Prune expired records

When the caller asks to prune with a cutoff time and a batch size of 1 to 1000 records, the server SHALL delete the state.write decision records older than the cutoff, up to the batch size, ordered by record ID.

#### Scenario: Expired record is deleted
- **WHEN** a state.write record is older than the cutoff
- **THEN** the method deletes it and returns its ID

#### Scenario: Unexpired record stays
- **WHEN** a state.write record is newer than the cutoff
- **THEN** the record still reads

#### Scenario: Other record stays
- **WHEN** a record of another decision point is older than the cutoff
- **THEN** the method does not delete it

#### Scenario: Batch is bounded
- **WHEN** more records than the batch size are expired
- **THEN** the method deletes only the batch size, lowest IDs first

### Requirement: Record every evaluation

When the server evaluates state.write, the server SHALL write one decision record, also when no gate exists.

#### Scenario: No gate still records
- **WHEN** an evaluation allows with no gate and no limit
- **THEN** the server writes one record

#### Scenario: Record after prune
- **WHEN** an evaluation runs after a prune
- **THEN** the server writes a new record with a new ID

### Requirement: Held lock writes no record

When a lock request arrives while the workspace lock is held, the server SHALL skip the state.write evaluation.

#### Scenario: Retry on held lock
- **WHEN** a lock request arrives while the lock is held
- **THEN** the server answers 409 and writes no record
