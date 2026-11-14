## ADDED Requirements

### Requirement: Simulate drafts over records

When a caller with action namespace:read-gates on the namespace posts draft gates and draft approval rules with a record filter, the server SHALL give for each readable stored record the stored result and the new result.

#### Scenario: Draft gates and approval rules change one past refusal
- **WHEN** a caller with action namespace:read-gates on the namespace posts draft gates and draft approval rules with a decision point and a time range that matches stored records
- **THEN** the answer holds one row per replayed record with the stored result next to the new result, including the changed refusal.

### Requirement: No side effects

When a caller posts drafts to the simulate route, the server SHALL store nothing and change no change.

#### Scenario: Replay stores nothing
- **WHEN** a caller posts drafts to the simulate route
- **THEN** no gate, approval, job, or change order changes and no record is stored.

#### Scenario: Draft with write side effect
- **WHEN** a draft gate tries a store write during replay
- **THEN** the replay stores nothing and changes no change.

### Requirement: Replay filter and snapshot

When the server replays stored records, the server SHALL use one snapshot of the drafts with a timeout and skip each unreadable or schema-unknown record with no trace of it in the answer.

#### Scenario: One change not readable
- **WHEN** a stored record names a change the caller cannot read
- **THEN** the answer holds no row, no ID, no fact content and no count for that record.

#### Scenario: Schema version not readable
- **WHEN** a stored record holds a fact schema version the engine cannot read
- **THEN** the answer holds no row, no ID and no count for that record.

#### Scenario: Concurrent draft edit during replay
- **WHEN** drafts change while a replay runs
- **THEN** the answer reflects the snapshot at request start only.

#### Scenario: Replay exceeds its time
- **WHEN** a replay passes its timeout or the caller cancels it
- **THEN** the server stops the replay, stores nothing, and answers 504.

### Requirement: Bounded replay

When the time range matches more than 1,000 readable records, the server SHALL refuse the request and evaluate nothing.

#### Scenario: Range matches more than 1,000 readable records
- **WHEN** the time range matches more than 1,000 readable records
- **THEN** the server refuses with 422 and evaluates nothing.

### Requirement: Starter pack list and install

When a caller with action namespace:manage-gates on the namespace installs a starter pack, the server SHALL make each gate of the pack in evaluate mode only in one atomic install.

#### Scenario: Install starter pack
- **WHEN** a caller with action namespace:manage-gates on the namespace installs a starter pack version not yet installed
- **THEN** each made gate has mode evaluate and the pack ID and version are recorded.

#### Scenario: Pack asks for enforce
- **WHEN** any gate of the pack asks for a mode other than evaluate
- **THEN** the server refuses the whole install and makes no gate.

#### Scenario: Gate name already exists
- **WHEN** a pack gate uses a gate name that the namespace already holds
- **THEN** the server refuses the whole install and makes no gate.

#### Scenario: Same pack version installed again
- **WHEN** the caller installs the same pack ID and version a second time
- **THEN** the server refuses with 409 and makes no change.

#### Scenario: Two installs at the same time
- **WHEN** two callers install the same pack at the same time
- **THEN** one install makes the gates and the other is refused with no duplicate gate.

### Requirement: Pack wording

Where a starter pack names a control framework, the server SHALL ship text that says designed against that framework and never claims compliance.

#### Scenario: Pack text check
- **WHEN** CI checks the text of each shipped starter pack
- **THEN** the text says designed against the named framework and says nothing that claims compliance.

#### Scenario: Hostile pack at runtime
- **WHEN** a pack names a mode other than evaluate or text that claims compliance
- **THEN** the server refuses the load.
