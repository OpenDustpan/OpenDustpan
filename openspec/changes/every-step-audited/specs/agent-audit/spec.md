## ADDED Requirements

### Requirement: Audit delegation and reason of each delegated step

When an agent acts under a delegation, the server SHALL write one audit entry that names the delegation and the reason.

#### Scenario: Tool call under delegation
- **WHEN** An agent calls a tool under a delegation with the body field reason.
- **THEN** One audit entry exists that names the delegation and the reason alongside the agent block of audit-entry-has-no-agent.

### Requirement: Audit denied delegated call

When an agent calls outside the delegation scope or under a revoked or expired delegation, the server SHALL write one denial audit entry that names the delegation where known and the refusal cause.

#### Scenario: Call outside delegation scope
- **WHEN** An agent calls outside the delegation scope or under a revoked or expired delegation.
- **THEN** One denial audit entry exists that names the delegation where known and the refusal cause alongside the agent block of audit-entry-has-no-agent.

### Requirement: Validate reason

When a delegated call carries a missing reason, a reason above 500 Unicode code points, or a reason outside ASCII printable 0x20 to 0x7E, the server SHALL refuse with audit.reason-invalid.

#### Scenario: Invalid reason refused
- **WHEN** A delegated call carries a missing reason, a reason above 500 Unicode code points, or a reason outside ASCII printable 0x20 to 0x7E.
- **THEN** The server refuses with audit.reason-invalid, repeats nothing of the input, and writes no entry.

### Requirement: Refuse step when append fails

When the audit append of a delegated step fails, the server SHALL refuse the step with no effect.

#### Scenario: Audit append fails
- **WHEN** The audit append of a delegated step fails.
- **THEN** The step has no effect and no step entry exists.

### Requirement: Denial append failure refuses the call

When the denial append of a refused delegated call fails, the server SHALL still refuse the call with 503 audit.entry-read-unavailable.

#### Scenario: Denial append fails
- **WHEN** The denial append of a refused delegated call fails.
- **THEN** The call is still refused with 503 audit.entry-read-unavailable.

### Requirement: Read delegation and reason entries

When an authorized caller lists audit entries, the server SHALL return the delegation and the reason of each agent entry.

#### Scenario: List entries
- **WHEN** An authorized caller lists the entries of the namespace.
- **THEN** Each agent entry returns the delegation and the reason alongside the agent block of audit-entry-has-no-agent.

#### Scenario: Cross-namespace read denied
- **WHEN** A caller with only one namespace token reads entries of another namespace.
- **THEN** The server refuses with audit.entry-not-found.

### Requirement: Verify chain with delegation and reason

Where the namespace hash chain holds delegation entries, the server SHALL include each delegation and reason field in the chain verification.

#### Scenario: Chain check passes
- **WHEN** A caller checks the hash chain of the namespace.
- **THEN** The check passes with each delegation and reason field hashed in order.

#### Scenario: Tampered delegation entry
- **WHEN** A delegation or reason field was changed, removed or reordered.
- **THEN** The check fails and names the first bad sequence number.

### Requirement: Verify entries written before the change

Where a chain holds entries written before this change, the server SHALL verify each old entry with the hash input stored with it.

#### Scenario: Old entries verify
- **WHEN** A caller checks a chain that holds entries written before this change.
- **THEN** The check verifies old entries with version 1 input and new entries with version 2 input that adds the stored delegation and reason fields, and a version 2 entry with stripped delegation or reason fields fails verification.

### Requirement: Reads fail fast during a database gap

When a caller lists entries or checks the chain while the database is unavailable, the server SHALL answer 503 with Retry-After 5 and audit.entry-read-unavailable.

#### Scenario: Read during gap
- **WHEN** A caller lists entries or checks the chain while the database is unavailable.
- **THEN** The server answers 503 with Retry-After 5 and audit.entry-read-unavailable.

### Requirement: Audit installation-scope agent steps

When an agent acts on an installation object with no namespace, the server SHALL write one audit entry in the installation scope chain.

#### Scenario: Installation step audited
- **WHEN** An agent acts on an installation object with no namespace.
- **THEN** One entry exists in the installation scope chain with the delegation and the reason alongside the agent block of audit-entry-has-no-agent.
