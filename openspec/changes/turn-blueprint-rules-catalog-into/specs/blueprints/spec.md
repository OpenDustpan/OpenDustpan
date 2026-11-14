## ADDED Requirements

### Requirement: Evaluate blueprint gates

When a blueprint gate denies a self-service request, the server SHALL refuse the request and link the decision record of the evaluation in the refusal.

#### Scenario: Denied request
- **WHEN** a blueprint gate denies a self-service request
- **THEN** the server refuses the request and links the decision record

### Requirement: Engine fail closed

If the gate engine errors or times out, then the server SHALL deny the request.

#### Scenario: Engine error
- **WHEN** the gate engine errors
- **THEN** the server denies the request

### Requirement: Gate denial audit

When a blueprint gate denies a request, the server SHALL write one audit entry with the gate ID and the decision record ID.

#### Scenario: Blocked audit
- **WHEN** a gate denies a request
- **THEN** the audit log holds one entry with both IDs
