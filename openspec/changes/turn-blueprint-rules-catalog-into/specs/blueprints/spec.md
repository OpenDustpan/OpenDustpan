## ADDED Requirements

### Requirement: Blueprint gate refusal

When a blueprint gate denies a self-service request, the server SHALL refuse the request and link the decision record of the evaluation in the refusal.

#### Scenario: Denied request
- **WHEN** a blueprint gate denies a self-service request
- **THEN** the server refuses the request, names the input and links the decision record

### Requirement: Gate denial audit

When a blueprint gate denies a request, the server SHALL write one audit entry with the gate ID and the decision record ID.

#### Scenario: Blocked request audit
- **WHEN** a blueprint gate denies a request
- **THEN** the audit log holds one entry with the gate ID and the decision record ID

### Requirement: Gate allow audit

When a blueprint gate allows a request, the server SHALL write one audit entry with the gate ID and the decision record ID.

#### Scenario: Allowed request audit
- **WHEN** a blueprint gate allows a request
- **THEN** the audit log holds one entry with the gate ID and the decision record ID

### Requirement: Schema input refusal

When a request gives an input that the blueprint schema rejects, the server SHALL answer 422 and name the input.

#### Scenario: Bad input retry
- **WHEN** a request gives an input that the blueprint schema rejects
- **THEN** the server answers 422, names the input and accepts the fixed request as a new revision

### Requirement: Grant refusal

When a caller without the grant requests a unit, the server SHALL answer 403 and name the missing action and object.

#### Scenario: Missing grant
- **WHEN** a caller without the grant requests a unit
- **THEN** the server answers 403 with the missing action and object

### Requirement: Engine fail closed

If the gate engine errors or times out, then the server SHALL deny the request and link the decision record of the failed evaluation.

#### Scenario: Engine error
- **WHEN** the gate engine errors or times out
- **THEN** the server denies the request and links the decision record

### Requirement: Single record

When two evaluations run with the same idempotency key at the same time, the server SHALL keep one decision record for the revision.

#### Scenario: Concurrent evaluation
- **WHEN** two evaluations run with the same idempotency key at the same time
- **THEN** one decision record exists for the revision

### Requirement: Gate warn

When a blueprint gate warns on a request, the server SHALL allow the request and show the warning with the decision record ID.

#### Scenario: Warned request
- **WHEN** a blueprint gate warns on a request
- **THEN** the server allows the request and shows the warning with the decision record ID

### Requirement: Other namespace blueprint

When a request names a blueprint of another namespace, the server SHALL answer 404 as for a name that does not exist.

#### Scenario: Cross-namespace request
- **WHEN** a request names a blueprint of another namespace
- **THEN** the server answers 404

### Requirement: Atomic audit

If the audit write of a gate evaluation fails, then the server SHALL roll back the evaluation write in the same transaction.

#### Scenario: Audit failure
- **WHEN** the audit write of a gate evaluation fails
- **THEN** the server rolls back the evaluation write and answers 500

### Requirement: Empty gate set

When no blueprint gate exists for a request, the server SHALL allow the request and write one decision record for the empty evaluation.

#### Scenario: No gates
- **WHEN** no blueprint gate exists for a request
- **THEN** the server allows the request and writes one decision record
