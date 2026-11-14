## ADDED Requirements

### Requirement: Serve scope routes

The server SHALL serve the gate, approval-rule and approval-default routes at the namespace, collection and by-name unit scopes with the read and manage actions of the namespace.

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

#### Scenario: Collection action
- **WHEN** a holder of the namespace manage action saves a gate on a collection
- **THEN** the server stores the gate at the collection scope

### Requirement: Installation scope

Where a gate route addresses the installation, the server SHALL check the installation read or manage gate action.

#### Scenario: Installation read
- **WHEN** a holder of the installation read action lists the gates of the installation
- **THEN** the server answers the installation gates

#### Scenario: Namespace manager
- **WHEN** a holder of only the namespace manage action writes an installation gate
- **THEN** the server answers 403 with code gate.denied

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

#### Scenario: Unknown unit
- **WHEN** a caller lists the gates of a name that no unit holds
- **THEN** the server answers 404 with code gate.not-found

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
- **THEN** the server answers 428 and stores no change

#### Scenario: Create
- **WHEN** a manager saves an object that does not exist, with no If-Match
- **THEN** the server stores the object

### Requirement: Limits

If a save makes a scope hold more than 256 gates or 64 approval rules, then the server SHALL refuse it with the code gate.limit.

#### Scenario: Over gate limit
- **WHEN** a scope holds 256 gates and a save adds one
- **THEN** the server answers 422 with code gate.limit

#### Scenario: Over rule limit
- **WHEN** a scope holds 64 approval rules and a save adds one
- **THEN** the server answers 422 with code gate.limit

#### Scenario: Condition size
- **WHEN** a save carries a condition above 4 KiB
- **THEN** the server answers 422 with code gate.limit

### Requirement: ID grammar

If an ID does not match the gate ID grammar, then the server SHALL refuse the save with the code gate.invalid.

#### Scenario: Bad ID
- **WHEN** a save names an ID with a capital letter or a space
- **THEN** the server answers 422 with code gate.invalid and stores no change

### Requirement: Rule resolution

When a gate names approval:<id>, the server SHALL resolve the rule at the scope of the gate and then at each wider scope.

#### Scenario: Wider rule
- **WHEN** a unit gate names a rule that only the namespace holds
- **THEN** the server resolves the namespace rule

#### Scenario: Narrower rule
- **WHEN** a namespace gate names a rule that only a unit holds
- **THEN** the server does not resolve it and refuses the save

### Requirement: Final gate

If a narrower scope saves an object with the ID of a wider final object, then the server SHALL refuse the save with the code gate.final.

#### Scenario: Shadow save
- **WHEN** a unit manager saves a rule with the ID of a final namespace rule
- **THEN** the server answers 409 with code gate.final

#### Scenario: Weaker final gate
- **WHEN** a holder of the manage action of its own scope makes a final gate weaker without a reason
- **THEN** the server refuses the save and stores no change

#### Scenario: Weaker with reason
- **WHEN** the holder gives a reason
- **THEN** the audit entry holds the reason

### Requirement: Locks held

If a lock is held on the unit, then the server SHALL refuse the save of a gate with 409.

#### Scenario: Held lock
- **WHEN** a manager saves a gate on a unit that a workspace lock holds
- **THEN** the server answers 409 and stores no change

### Requirement: Audit

The server SHALL write a change audit entry with the old and the new version in the transaction of each create, change and delete.

#### Scenario: Atomic entry
- **WHEN** the audit write fails
- **THEN** the server stores no change

### Requirement: Fresh authentication

If a change to a final gate, an approval rule or an approval default has no fresh authentication, then the server SHALL refuse it with the code mfa.fresh-step-required.

#### Scenario: Access token
- **WHEN** a caller with an access token saves an approval rule
- **THEN** the server answers with code mfa.fresh-step-required and stores no change

#### Scenario: Old session
- **WHEN** a console session with an old sign-in saves an approval default
- **THEN** the server answers with code mfa.fresh-step-required

#### Scenario: Fresh session
- **WHEN** a session inside the fresh window saves an approval default
- **THEN** the server stores it

### Requirement: Unavailable

If the server cannot read the gate configuration, then the server SHALL answer 503 with the code gate.unavailable.

#### Scenario: Store down
- **WHEN** the store fails on a read
- **THEN** the server answers 503 with code gate.unavailable

### Requirement: Default reset

When a holder of the manage action deletes the approval default, the server SHALL answer 204 also when none is set.

#### Scenario: Reset set default
- **WHEN** a manager deletes an approval default that is set
- **THEN** the server answers 204 and the scope has no default

#### Scenario: Reset none
- **WHEN** a manager deletes the approval default of a scope that has none
- **THEN** the server answers 204

### Requirement: Carry-over setting

When a holder of the manage action saves the signature carry-over setting of a scope, the server SHALL store it and show its effective value on read.

#### Scenario: Read default
- **WHEN** a holder of the read action reads a scope that has no saved setting
- **THEN** the server answers the default value

### Requirement: Backup

When the server makes a backup, the server SHALL include the gate configuration rows.

#### Scenario: Round trip
- **WHEN** a backup with gates and approval rules is restored
- **THEN** the same gates and rules exist after the restore
