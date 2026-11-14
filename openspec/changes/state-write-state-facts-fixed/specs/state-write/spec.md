## ADDED Requirements

### Requirement: State facts from both states

When a tool-neutral operation holds the old and the new state, the server SHALL fill state.resources from the new state and state.removed from the removed resources.

#### Scenario: Update with removals
- **WHEN** A tool-neutral operation holds the old and the new state
- **THEN** The fact document holds state.available true, state.resources and state.removed.

#### Scenario: Concurrent write
- **WHEN** A concurrent write changes the stored state before commit
- **THEN** Both the step and the allow record roll back.

### Requirement: Client-encrypted body continues

When the state body is missing or client-encrypted, the server SHALL continue the write with state.available false and the reason recorded.

#### Scenario: Encrypted body
- **WHEN** The state body is client-encrypted
- **THEN** The facts hold state.available false with the reason and the write continues.

### Requirement: Truncated counts fail closed

When the state holds more than 10000 counted resources, the server SHALL mark the facts truncated and fail threshold comparisons closed.

#### Scenario: Above the cap
- **WHEN** The state holds 20000 resources
- **THEN** The facts hold truncated true and a threshold comparison gives an error result.

### Requirement: Unlocked write

When an unlocked state version arrives through the TFE-compatible API, the server SHALL evaluate the state.write gates with operation kind unlocked-write before its transaction.

#### Scenario: Unlocked version
- **WHEN** An unlocked state version arrives
- **THEN** The gates run with kind unlocked-write and a refusal refuses the write.

### Requirement: Allow record linkage

When a state.write evaluation allows the operation, the server SHALL write the allow record in the same transaction as the step.

#### Scenario: Allowed write
- **WHEN** The evaluation allows the operation
- **THEN** The step commits with its record and the audit entry names the record ID.

### Requirement: Refusal record

When a state.write evaluation refuses or waits, the server SHALL write the record in its own transaction.

#### Scenario: Denied write
- **WHEN** The evaluation denies the operation
- **THEN** The refusal record commits and the error names the record ID.

#### Scenario: Store down
- **WHEN** The refusal record cannot commit
- **THEN** The client gets 503 gate.unavailable and no state changes.

### Requirement: Rollback

When the step does not commit, the server SHALL roll back the allow record with it.

#### Scenario: Fence lost
- **WHEN** The store loses the fence before commit
- **THEN** No record claims the step committed.

### Requirement: Fixed controls in record

When the server evaluates state.write, the server SHALL list the workspace lock, manual lock, freeze, override switch and safe delete states in the decision record.

#### Scenario: Locked workspace
- **WHEN** A write runs on a locked workspace
- **THEN** The record lists each fixed control state.

### Requirement: Gate error

When a gate in enforce mode errors at state.write, the server SHALL stop the write with gate.rule-failed.

#### Scenario: Enforce error
- **WHEN** A gate in enforce mode errors
- **THEN** The write stops with 409 gate.rule-failed.

#### Scenario: Evaluate error
- **WHEN** A gate in evaluate mode errors
- **THEN** The write continues and the record holds the error result.

### Requirement: Approval defaults

When an approval default applies at state.write, the server SHALL require its distinct eligible approvers before the write proceeds.

#### Scenario: Two approvers
- **WHEN** The default needs two approvers
- **THEN** The write waits for two distinct eligible signatures.

#### Scenario: Author signs
- **WHEN** The author signs
- **THEN** The signature does not count.

### Requirement: Limits on every route

When any route asks for an action on a unit, namespace, collection or installation object, the server SHALL evaluate the access limits before the call proceeds.

#### Scenario: Limit on unit
- **WHEN** A limit refuses a call on a unit
- **THEN** The call returns 403 gate.limited with the record ID.

#### Scenario: Limit on namespace
- **WHEN** A limit refuses a call on a namespace
- **THEN** The call returns 403 gate.limited.

### Requirement: Break-glass

While a break-glass session calls a route, the server SHALL skip the access limits and hold the gates, approval rules and fixed controls.

#### Scenario: Break-glass call
- **WHEN** A break-glass session calls a route
- **THEN** Access limits are skipped and gates hold.
