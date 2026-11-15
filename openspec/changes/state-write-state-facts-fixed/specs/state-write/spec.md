## ADDED Requirements

### Requirement: State facts from both states

When a tool-neutral operation holds the old and the new state, the server SHALL fill state.resources from the new state and state.removed from the removed resources.

#### Scenario: Update with removals
- **WHEN** an operation holds the old and the new state
- **THEN** the facts hold state.available true, state.resources and state.removed

### Requirement: Client-encrypted body

When the state body is client-encrypted, the server SHALL continue the write with state.available false and the reason recorded.

#### Scenario: Encrypted body
- **WHEN** the state body is client-encrypted
- **THEN** the facts hold state.available false with the reason and the write continues

### Requirement: Truncated counts

When the state holds more than 10000 counted resources, the server SHALL fail threshold comparisons closed.

#### Scenario: Above the cap
- **WHEN** the state holds 20000 resources
- **THEN** a threshold comparison gives an error result

### Requirement: Allow record

When a state.write evaluation allows the operation, the server SHALL write the allow record in the same transaction as the step.

#### Scenario: Allowed write
- **WHEN** the evaluation allows the operation
- **THEN** the step commits with its record and the audit entry names the record ID

#### Scenario: Fence lost
- **WHEN** the store loses the fence before commit
- **THEN** no record claims the step committed

### Requirement: Refusal record

When a state.write evaluation refuses the operation, the server SHALL write the record in its own transaction.

#### Scenario: Denied write
- **WHEN** the evaluation denies the operation
- **THEN** the refusal record commits and the error names the record ID

#### Scenario: Store down
- **WHEN** the refusal record cannot commit
- **THEN** the client gets 503 gate.unavailable and no state changes

### Requirement: Fixed controls

When the server evaluates state.write, the server SHALL list the workspace lock, manual lock, freeze, override switch and safe delete states in the record.

#### Scenario: Locked workspace
- **WHEN** a write runs on a locked workspace
- **THEN** the record lists each fixed control state

### Requirement: Approval defaults

When an approval default applies at state.write, the server SHALL require its distinct eligible approvers before the write proceeds.

#### Scenario: Author signs
- **WHEN** the author signs
- **THEN** the signature does not count

### Requirement: Limits on every route

When any route asks for an action on a unit, namespace, collection or installation object, the server SHALL evaluate the access limits before the call proceeds.

#### Scenario: Limit on namespace
- **WHEN** a limit refuses a call on a namespace
- **THEN** the call returns 403 gate.limited

### Requirement: Break-glass

While a break-glass session calls a route, the server SHALL skip the access limits and hold the gates.

#### Scenario: Break-glass call
- **WHEN** a break-glass session calls a route
- **THEN** access limits are skipped and gates hold
