## ADDED Requirements

### Requirement: forbid-only-save

When a caller saves an access limit with effect permit, the server SHALL refuse the save with gate.ErrInvalid.

#### Scenario: permit body refused
- **WHEN** a caller saves a limit with effect permit
- **THEN** the server answers 400 with gate.ErrInvalid and stores nothing

### Requirement: collection forbid wins

When grants allow an action on a unit in a collection with a matching forbid limit, the server SHALL refuse the call with gate.ErrLimited.

#### Scenario: unit call under collection forbid
- **WHEN** grants allow an action on a unit with a matching collection forbid
- **THEN** the call gets 403 with gate.ErrLimited that names the limit ID

### Requirement: scope-addition

When grants allow an action on an object with a matching limit at any scope that holds the object, the server SHALL refuse the call with gate.ErrLimited.

#### Scenario: narrower limit adds to wider limit
- **WHEN** grants allow an action with no matching namespace limit but a matching unit limit
- **THEN** the call gets 403 with gate.ErrLimited that names the unit limit ID

### Requirement: gate-config-guard

When a caller saves a limit that matches installation gate management, the server SHALL refuse the save with gate.ErrInvalid.

#### Scenario: gate lockout limit refused
- **WHEN** a caller saves a namespace limit that matches installation gate management
- **THEN** the server answers 400 with gate.ErrInvalid and stores nothing

### Requirement: broad-forbid-allowed

When a caller saves a forbid with no principal condition that passes the gate-configuration guard, the server SHALL store the limit.

#### Scenario: broad forbid saves
- **WHEN** a caller saves a namespace forbid with no principal condition that passes the gate guard
- **THEN** the server stores the limit and later refuses each matching call with gate.ErrLimited

### Requirement: label-limit

When a limit reads a unit label, the server SHALL apply the limit only while the label stays on the unit.

#### Scenario: label removal lifts limit
- **WHEN** a holder of the label-edit action removes the unit label that the limit reads
- **THEN** the previously refused action passes the limit check

### Requirement: fail-closed-evaluation

When evaluation of an access limit gives a Cedar error, the server SHALL refuse the call with gate.ErrRuleFailed.

#### Scenario: cedar error refuses
- **WHEN** a matching limit gives a Cedar evaluation error
- **THEN** the call gets 403 with gate.ErrRuleFailed and no allow

### Requirement: refusal-decision-record

When an access limit refuses a call, the server SHALL write a decision record that names the principal, the action and the limit ID.

#### Scenario: repeated refusal shares a record
- **WHEN** the same principal repeats the same refused action on the same limit within 60 seconds
- **THEN** the server writes one decision record with a counter and the time of the last refusal

### Requirement: record-write-fails-closed

When the decision-record write for a limit refusal fails, the server SHALL still refuse the call.

#### Scenario: record write fails
- **WHEN** the decision-record write for a limit refusal fails
- **THEN** the call still gets 403 with gate.ErrLimited

### Requirement: limit-change-refusal

When a caller without the scope action or without a fresh step changes an access limit, the server SHALL refuse the call with 403.

#### Scenario: change without scope right refused
- **WHEN** a caller without the scope action or without a fresh step changes a limit
- **THEN** the server answers 403 and stores no change

### Requirement: break-glass-exempt

When a live session of an enabled break-glass account calls an action with a matching forbid limit, the server SHALL allow the call past the limits.

#### Scenario: break-glass session allowed
- **WHEN** a live session of an enabled break-glass account calls an action with a matching forbid limit
- **THEN** the call passes the limits and the audit entry states the exemption

### Requirement: limit change audit

When a caller changes an access limit, the server SHALL write an audit entry with the actor, the limit ID and the scope.

#### Scenario: set limit
- **WHEN** a holder of the scope action with a fresh step sets a limit
- **THEN** the audit log holds the actor, the limit ID, the scope and the new policy
