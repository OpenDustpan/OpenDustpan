## ADDED Requirements

### Requirement: Issue request uses chain

When an Issue arrives from an address outside the allowlist, the server SHALL refuse the Issue with error code access.ip-not-allowed.

#### Scenario: Outside allowlist refused
- **WHEN** an Issue arrives from an address outside the allowlist with an embedder authorizer that allows everything
- **THEN** the server refuses the Issue with error code access.ip-not-allowed.

#### Scenario: Cross-namespace service refused
- **WHEN** a namespace-service principal requests an Issue for another namespace with an embedder authorizer that allows everything
- **THEN** the server refuses the Issue with error code access.denied.

### Requirement: Token escalation refused

When a caller requests an Issue with more reach than the caller has, the server SHALL refuse the Issue with error code access.denied.

#### Scenario: Above reach refused
- **WHEN** a caller requests an Issue with more reach than the caller has with an embedder authorizer that allows everything
- **THEN** the server refuses the Issue with error code access.denied.

### Requirement: Token fail closed

If the client address is missing or the allowlist store read fails, then the server SHALL deny the Issue with error code access.denied.

#### Scenario: Missing address refused
- **WHEN** an Issue arrives with no client address at the service layer
- **THEN** the server refuses the Issue with error code access.denied.

#### Scenario: Store error denies
- **WHEN** the allowlist store read fails during an Issue check
- **THEN** the server denies the Issue with error code access.denied.
