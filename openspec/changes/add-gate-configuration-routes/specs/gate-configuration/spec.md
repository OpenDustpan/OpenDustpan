## ADDED Requirements

### Requirement: Serve routes

The server SHALL serve the gate, approval-rule and approval-default routes at the installation, namespace, collection and by-name unit scopes with the read and manage actions of each scope.

#### Scenario: List gates
- **WHEN** a holder of the read action lists the gates of a namespace
- **THEN** the server answers the gates of that namespace only

#### Scenario: Other scope
- **WHEN** a caller reads an object ID of another scope
- **THEN** the server answers 404 with code gate.not-found

#### Scenario: No action
- **WHEN** a caller without the manage action writes a gate
- **THEN** the server answers 403 with code gate.denied and the record ID

#### Scenario: Unit path
- **WHEN** a holder of the read action lists the gates of the stack web/prod through the by-name path with the segment encoded
- **THEN** the server answers the gates of that unit

### Requirement: Stable unit key

The server SHALL key the gate rows of a unit on the stable unit ID.

#### Scenario: Reused name
- **WHEN** a unit is deleted and a new unit takes its name
- **THEN** the new unit has no gate rows of the deleted unit

#### Scenario: Rename
- **WHEN** a unit is renamed
- **THEN** its gate rows stay with it

#### Scenario: Restore
- **WHEN** a deleted unit is restored
- **THEN** its gate rows return with it

### Requirement: Stale write

If the If-Match version is not the stored version, then the server SHALL refuse the write with the code gate.version-conflict.

#### Scenario: Stale version
- **WHEN** two managers write with the same version
- **THEN** the second write gets 409 and the first stays

#### Scenario: Retry
- **WHEN** the second manager reads the object again and writes with the new version
- **THEN** the write succeeds

#### Scenario: No If-Match
- **WHEN** a write to an existing object carries no If-Match
- **THEN** the server refuses the write and stores no change

### Requirement: Limits

If a save exceeds a limit of the scope, then the server SHALL refuse it with the code gate.limit.

#### Scenario: Over limit
- **WHEN** a scope holds 256 gates and a save adds one
- **THEN** the server answers 422 with code gate.limit

### Requirement: Audit

The server SHALL write a change audit entry with the old and the new version in the transaction of each create, change and delete.

#### Scenario: Atomic entry
- **WHEN** the audit write fails
- **THEN** the server stores no change

### Requirement: Fresh authentication

If a change needs fresh authentication and the caller has none, then the server SHALL refuse it with the code mfa.fresh-step-required.

#### Scenario: Access token
- **WHEN** a caller with an access token changes a gate that needs fresh authentication
- **THEN** the server answers with code mfa.fresh-step-required and stores no change

### Requirement: Carry-over setting

When a holder of the manage action saves the signature carry-over setting of a scope, the server SHALL store it and show its effective value on read.

#### Scenario: Read default
- **WHEN** a holder of the read action reads a scope that has no saved setting
- **THEN** the server answers the default value
