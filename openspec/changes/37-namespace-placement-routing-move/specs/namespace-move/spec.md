## ADDED Requirements

### Requirement: Start move

When an operator with a fresh authentication and the action installation:move-namespace posts a target from the peer list, the server SHALL create one move in state started.

#### Scenario: Operator starts move
- **WHEN** the operator posts a listed target
- **THEN** the server answers 202 with the move ID and writes the audit entry namespace.move-start

#### Scenario: Unlisted target
- **WHEN** the operator posts a target that is not in the peer list
- **THEN** the server answers 400 with namespace.move-target-unknown and starts no copy

#### Scenario: No fresh step
- **WHEN** the caller has no fresh authentication
- **THEN** the server answers 403 with mfa.fresh-step-required

### Requirement: One live move

If a move is live for the namespace, then the server SHALL refuse a second start with the error code namespace.move-conflict.

#### Scenario: Second start refused
- **WHEN** the operator starts a second move
- **THEN** the server answers 409 with namespace.move-conflict

### Requirement: Writes during move

While a move is in state started or copying, the server SHALL refuse namespace writes on the source with the error code namespace.move-in-progress.

#### Scenario: Write blocked
- **WHEN** a write reaches the source in state copying
- **THEN** the server answers 423 with namespace.move-in-progress

### Requirement: Write block limit

If a move runs more than 30 minutes, then the server SHALL fail the move and roll back.

#### Scenario: Limit reached
- **WHEN** the copy passes 30 minutes
- **THEN** the state is rolled-back, source writes succeed and the audit entry namespace.move-fail exists

### Requirement: Name conflict

If the target holds a collection of the same name with another stable ID, then the server SHALL refuse the start with the error code collection.name-conflict.

#### Scenario: Same-name collection
- **WHEN** the target holds that collection
- **THEN** the server answers 409 and names the collection

### Requirement: Carry defaults

When a move starts, the server SHALL carry the default collection and the catch-all placement rule by stable ID when they exist.

#### Scenario: None to carry
- **WHEN** the namespace has no default collection and no catch-all rule
- **THEN** the move completes and the target holds neither

### Requirement: Complete move

When the target verifies the staged import, the server SHALL run CompleteMove to commit the target as primary with the failover version of the source plus one and then demote the source.

#### Scenario: Single handoff
- **WHEN** CompleteMove runs after the target verifies the import
- **THEN** the target is primary, the version is source plus one, old-fence writes get ErrFenced and the audit entry namespace.move-complete exists

### Requirement: Background work

When the handoff completes, the server SHALL run durable operations, schedules and retention of the namespace only on the new primary.

#### Scenario: Work stops on old primary
- **WHEN** the move completes
- **THEN** the source runs none of them for the namespace

### Requirement: Rollback

If staged verification fails or the operator posts a rollback before the target commit, then the server SHALL run RollbackMove to restore the source as primary.

#### Scenario: Failed verify
- **WHEN** verification fails
- **THEN** RollbackMove keeps the source primary, deletes the staged data and writes the audit entry namespace.move-rollback

#### Scenario: Rollback after completed
- **WHEN** the operator posts a rollback for a completed move
- **THEN** the server answers 409 with namespace.move-not-reversible

### Requirement: Move status

When an operator with the action reads a move, the server SHALL answer its state, its target and its copied bytes.

#### Scenario: Read status
- **WHEN** the operator calls GET moves/{move}
- **THEN** the answer holds state, target and copied bytes in under 300 ms at p95
