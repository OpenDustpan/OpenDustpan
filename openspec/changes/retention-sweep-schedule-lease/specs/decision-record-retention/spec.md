## ADDED Requirements

### Requirement: Scheduled prune of decision records

While the worker component runs on a server with a durable store, the server SHALL prune the `state.write` decision records that are older than the retention period of `decisionRecords.retentionHours`, in each namespace, once for each period of one hour.

#### Scenario: Expired record
- **WHEN** a `state.write` decision record is older than the retention period and the schedule fires
- **THEN** the prune deletes the record and writes one audit entry for each batch with the count of the deleted records

#### Scenario: Record that is not expired
- **WHEN** a record is newer than the retention period, or is at another decision point, or is in another namespace than the batch
- **THEN** the prune keeps the record

#### Scenario: Keep forever
- **WHEN** the retention setting is `0`
- **THEN** the prune deletes nothing and writes no audit entry

#### Scenario: No durable store
- **WHEN** the server has no durable store
- **THEN** the server runs no prune

### Requirement: One prune at a time

The server SHALL run the prune in one process for each tick of the schedule.

#### Scenario: Two processes
- **WHEN** two server processes on one database fire the schedule together
- **THEN** one process prunes and the other does not run the schedule at that tick

### Requirement: The prune fails closed

If the stored retention text is not `0` or a whole number of hours from 24 to 87600, or the setting cannot be read, then the server SHALL delete no record and SHALL fail the run. The prune SHALL NOT delete a record in the server scope, and SHALL skip a namespace whose fence is stale.

#### Scenario: Refused retention text
- **WHEN** the stored text is `-1`, `23`, `087600` or empty
- **THEN** the run fails, logs the key and the allowed range, and deletes nothing

#### Scenario: Stale fence
- **WHEN** one namespace has a stale fence during a run
- **THEN** the prune skips that namespace, logs its name and still prunes the other namespaces
