## ADDED Requirements

### Requirement: Stack not-found arguments

When a stack read finds no stack, the server SHALL carry the namespace, project and stack arguments of the requested reference.

#### Scenario: Unknown stack
- **WHEN** a person reads an absent stack through Service.GetStack
- **THEN** the answer carries stack.not-found with namespace, project and stack

### Requirement: Stack already-exists arguments

When a stack create or rename collides, the server SHALL carry the namespace, project and stack arguments of the new reference.

#### Scenario: Create collision
- **WHEN** a person creates a stack that exists
- **THEN** the answer carries stack.already-exists with the new reference

#### Scenario: Rename collision
- **WHEN** a person renames a stack to an existing name
- **THEN** the answer carries stack.already-exists with the new reference

### Requirement: Stack no-state arguments

When a stack has no state for a read, the server SHALL carry the namespace, project and stack arguments of the requested reference.

#### Scenario: No checkpoint
- **WHEN** a person reads a stack with no checkpoint through Updates.ExportAt
- **THEN** the answer carries stack.no-state with namespace, project and stack

#### Scenario: Before first state
- **WHEN** a person reads a time before the first state through Updates.ExportAt
- **THEN** the answer carries stack.no-state-at-time with namespace, project, stack and first

### Requirement: Reveal path argument

When a reveal finds no value at its path, the server SHALL carry the path argument.

#### Scenario: Reveal path
- **WHEN** a person reveals a path that has no value
- **THEN** the answer carries stack.resource-not-found with path

#### Scenario: URN read
- **WHEN** a person reads a resource URN that has no value and the URN passes the identifier check
- **THEN** the answer carries stack.resource-not-found with path holding the URN

### Requirement: Update identity arguments

When an update read, start or delete finds an unknown or active update, the server SHALL carry the update argument.

#### Scenario: Unknown update
- **WHEN** a person reads an absent update
- **THEN** the answer carries update.not-found with update

#### Scenario: Running update
- **WHEN** a person starts an update while one runs
- **THEN** the answer carries update.conflict with the active update

#### Scenario: Delete refused on SQLite
- **WHEN** a person deletes a stack while an update runs on SQLite
- **THEN** the answer carries update.conflict with the active update

#### Scenario: Delete refused on PostgreSQL
- **WHEN** a person deletes a stack while an update runs on PostgreSQL
- **THEN** the answer carries update.conflict with the active update

### Requirement: Update state and lease arguments

When an update start or write finds a wrong state or an expired lease, the server SHALL carry the state or expiredAt argument.

#### Scenario: Start of a succeeded update
- **WHEN** a person starts an update that succeeded
- **THEN** the answer carries update.wrong-state with state

#### Scenario: Preview write
- **WHEN** a person saves state to a preview
- **THEN** the answer carries update.wrong-state with state preview

#### Scenario: Expired lease
- **WHEN** a person writes with an expired lease
- **THEN** the answer carries update.lease-expired with expiredAt

### Requirement: Update size arguments

When a journal batch or a replay exceeds its bound, the server SHALL carry the bound, size and limit arguments.

#### Scenario: Journal batch
- **WHEN** a person sends a batch above the limit
- **THEN** the answer carries update.too-large with bound batch, size and limit

#### Scenario: Replay bound
- **WHEN** a replay passes its bound
- **THEN** the answer carries update.too-large with bound, size and limit

### Requirement: Corrupt active row

If the stored active update ID fails the identifier check, then the server SHALL answer server.internal with no update argument.

#### Scenario: Bad row on SQLite
- **WHEN** the store reads a bad active ID on SQLite
- **THEN** the answer carries server.internal

#### Scenario: Bad row on PostgreSQL
- **WHEN** the store reads a bad active ID on PostgreSQL
- **THEN** the answer carries server.internal

### Requirement: Wrapped error precedence

When an error chain holds more than one typed error, the server SHALL carry the arguments of the innermost typed error.

#### Scenario: Nested typed errors
- **WHEN** a service wraps a store typed error in its own typed error
- **THEN** the answer carries the arguments of the innermost error
