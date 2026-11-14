## ADDED Requirements

### Requirement: Create with kind

When a caller creates a principal, the server SHALL store one kind of person, service, or the NEW agent kind.

#### Scenario: Create agent principal
- **WHEN** a site administrator creates a principal with the NEW kind agent
- **THEN** the server stores kind agent and returns it on read.

### Requirement: Reject unknown kind

If a caller sends an unknown principal kind, then the server SHALL reject the request with the NEW error code principal.kind-invalid.

#### Scenario: Reject robot kind
- **WHEN** a caller sends kind robot
- **THEN** the server rejects the request with the NEW error code principal.kind-invalid.

### Requirement: Default omitted kind

When a caller creates a principal without a kind, the server SHALL store kind person.

#### Scenario: Omit kind on create
- **WHEN** a caller creates a principal without a kind
- **THEN** the server stores kind person and returns it on read.

### Requirement: Freeze kind after creation

If a caller updates the stored kind of a principal, then the server SHALL reject the request with the NEW error code principal.kind-immutable.

#### Scenario: Change kind after creation
- **WHEN** a caller updates the kind from person to agent
- **THEN** the server rejects the request with the NEW error code principal.kind-immutable.

### Requirement: Read legacy principal

When a stored principal carries no kind value, the server SHALL return kind person.

#### Scenario: Read row without kind
- **WHEN** a stored principal carries no kind value
- **THEN** the server returns kind person and keeps current access unchanged.
