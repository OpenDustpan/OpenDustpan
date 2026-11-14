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
- **WHEN** a stored record names a change the caller cannot read with the read action of that change
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

When the time range matches more than 1,000 stored records before the readability check, the server SHALL refuse the request and evaluate nothing.

#### Scenario: Range matches more than 1,000 records
- **WHEN** the time range matches more than 1,000 stored records before the readability check
- **THEN** the server refuses with 422 and evaluates nothing.

### Requirement: Invalid simulate input

When the simulate request holds an unknown decision point, a bad time range, invalid draft syntax, or no draft, the server SHALL refuse the request and evaluate nothing.

#### Scenario: Unknown decision point or bad time range
- **WHEN** the filter names an unknown decision point or a start time after its end time
- **THEN** the server refuses with 422 and evaluates nothing.

#### Scenario: Invalid draft syntax or empty drafts
- **WHEN** a draft gate or draft approval rule has invalid syntax or the request holds no draft
- **THEN** the server refuses with 422 and evaluates nothing.

### Requirement: Empty result and fact integrity

When the server replays stored records, the server SHALL skip each record with a fact hash mismatch with no trace and replay draft approval rules against the stored signatures.

#### Scenario: No records matched
- **WHEN** the filter matches zero stored records
- **THEN** the answer holds zero rows and stores nothing.

#### Scenario: Fact document hash mismatch
- **WHEN** a stored fact document does not match its SHA-256
- **THEN** the answer holds no row, no ID and no count for that record.

#### Scenario: Draft approval replay
- **WHEN** the request holds draft approval rules
- **THEN** the new result reflects the draft approval rules against the stored signatures and eligible approvers.

### Requirement: Starter pack list and install

When a caller with action namespace:manage-gates on the namespace installs a starter pack, the server SHALL make each gate of the pack in evaluate mode only with its approval rules in one atomic install.

#### Scenario: List starter packs
- **WHEN** a caller with action namespace:read-gates on the namespace lists starter packs
- **THEN** the answer lists each shipped pack with its ID, version, and designed-against framework.

#### Scenario: Install starter pack
- **WHEN** a caller with action namespace:manage-gates on the namespace installs a starter pack version not yet installed
- **THEN** each made gate has mode evaluate, each pack approval rule is installed, and the pack ID and version are recorded.

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

#### Scenario: Unknown pack ID
- **WHEN** the caller installs a pack ID the server does not ship
- **THEN** the server answers 404 and makes no change.

### Requirement: Pack wording

Where a starter pack names a control framework, the server SHALL ship text that says designed against that framework and never claims compliance.

#### Scenario: Pack text check
- **WHEN** CI checks the text of each shipped starter pack
- **THEN** the text says designed against the named framework and says nothing that claims compliance.

#### Scenario: Hostile pack at runtime
- **WHEN** a pack names a mode other than evaluate or text that claims compliance
- **THEN** the server refuses the load.

### Requirement: Pack gate test files

Where a starter pack ships a gate, the server SHALL ship a test file for that gate that gate.RunTestFile runs in CI.

#### Scenario: Gate without test file
- **WHEN** a pack gate has no test file that gate.RunTestFile runs
- **THEN** the server refuses the pack load and CI fails.
