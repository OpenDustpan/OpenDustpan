## ADDED Requirements

### Requirement: Retain state.write records for audit retention

While a decision record of state.write exists, the server SHALL keep it until the audit retention passes and then delete it in the retention sweep.

#### Scenario: Expired allow record is pruned
- **WHEN** The retention sweep runs after the audit retention passes for an allow record of state.write.
- **THEN** The record no longer reads through the record read route and the prune audit entry names its ID.

#### Scenario: Unexpired record is kept
- **WHEN** The retention sweep runs before the audit retention passes.
- **THEN** The record still reads with its fact document and gate outcomes.

#### Scenario: Expired refusal record is pruned
- **WHEN** The retention sweep runs after the audit retention passes for a refusal record written in its own transaction.
- **THEN** The refusal record no longer reads and the prune audit entry names its ID while the step audit entry keeps the ID.

#### Scenario: Expired unlocked-write record is pruned
- **WHEN** The retention sweep runs after the audit retention passes for a state.write record with operation kind unlocked-write.
- **THEN** The unlocked-write record no longer reads after the sweep.

#### Scenario: Open step is kept
- **WHEN** The sweep meets a state.write record whose explained step is still open.
- **THEN** The sweep keeps the record and deletes it only after the step completes and the retention passes.

#### Scenario: New write after prune still records
- **WHEN** A state.write evaluation runs after the sweep pruned its old records.
- **THEN** The new evaluation writes a new decision record with a new ID.

### Requirement: Record every evaluation

When the server evaluates state.write, the server SHALL write one decision record, also when no gate exists and the result is allow.

#### Scenario: Allow with no gate still records
- **WHEN** A state.write evaluation allows with no gate and no limit in the chain.
- **THEN** The server writes one decision record with the fact document and an empty gate list.

### Requirement: Held lock writes no record

When a lock request arrives while the workspace lock is held, the server SHALL skip the state.write evaluation.

#### Scenario: Retry against held lock writes nothing
- **WHEN** An OpenTofu lock request arrives while the workspace lock is held.
- **THEN** The server runs no evaluation and writes no decision record.

### Requirement: Prune keeps the audit reference

When the retention sweep deletes a decision record, the server SHALL keep the record ID in the audit entry of the explained step.

#### Scenario: Step audit entry keeps record ID
- **WHEN** The retention sweep deletes the decision record of a state.write step.
- **THEN** The audit entry of the explained step still names the record ID and the prune batch entry names the same ID.

#### Scenario: Missing step entry blocks prune
- **WHEN** The sweep meets an expired record with no step audit entry.
- **THEN** The sweep keeps the record and deletes it only after the step audit entry exists and the retention passes.

### Requirement: Sweep runs in safe batches

While the retention sweep runs, the server SHALL delete expired state.write records in bounded batches ordered by stable record ID with one lease holder at a time.

#### Scenario: Restart mid-batch is safe
- **WHEN** The sweeper restarts in the middle of a batch.
- **THEN** A rerun deletes only the records that still exist and never deletes an unexpired record.

#### Scenario: Two sweepers do not overlap
- **WHEN** Two server processes start the sweep at the same time.
- **THEN** Only one holder deletes records at a time through the store lease.

#### Scenario: Writes proceed during sweep
- **WHEN** A state.write evaluation runs while the sweep holds its lease.
- **THEN** The state write evaluates and writes its decision record with no block on the sweep lease.
