## ADDED Requirements

### Requirement: Grant request uses chain

When a Grant write arrives from an address outside the allowlist, the server SHALL refuse the write with error code access.ip-not-allowed.

#### Scenario: Outside allowlist refused
- **WHEN** a Grant write arrives from an address outside the allowlist with an embedder authorizer that allows everything
- **THEN** the server refuses the write with error code access.ip-not-allowed.

#### Scenario: Both gates fail
- **WHEN** a Grant write fails both the allowlist and the namespace-service scope
- **THEN** the server refuses the write with error code access.ip-not-allowed.

### Requirement: Grant cross-namespace service refused

When a namespace-service principal requests a Grant write, revoke or list outside its own namespace, the server SHALL refuse the Grant with error code access.denied.

#### Scenario: Other namespace refused
- **WHEN** a namespace-service principal requests a Grant write, revoke or list in another namespace with an embedder authorizer that allows everything
- **THEN** the server refuses the Grant with error code access.denied.

### Requirement: Grant escalation refused

When a caller requests a Grant above its own reach, the server SHALL refuse the Grant with error code access.denied.

#### Scenario: Above reach refused
- **WHEN** a caller requests a Grant above its own reach with an embedder authorizer that allows everything
- **THEN** the server refuses the Grant with error code access.denied.

#### Scenario: Chain denies first
- **WHEN** the chain denies a Grant with an authorizer that allows nothing
- **THEN** the server refuses the Grant with error code access.denied without reaching the escalation comparator.

### Requirement: Grant fail closed

If the client address is missing or the allowlist store read fails, then the server SHALL deny the Grant with error code access.denied.

#### Scenario: Missing address refused
- **WHEN** a Grant write arrives with no client address at the service layer
- **THEN** the server refuses the Grant with error code access.denied.

#### Scenario: Store error denies
- **WHEN** the allowlist store read fails during a Grant check
- **THEN** the server denies the Grant with error code access.denied.

### Requirement: Grant refusal audited

When the service refuses a Grant for allowlist, scope or escalation, the server SHALL write an audit entry of kind access for the denial.

#### Scenario: Refusal writes audit
- **WHEN** the service refuses a Grant for allowlist, scope or escalation
- **THEN** the server writes an audit entry of kind access for the denial.
