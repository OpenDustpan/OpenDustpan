## ADDED Requirements

### Requirement: Populate agent block

When the server appends an audit entry for a request authenticated as an agent-kind principal with a delegation, the server SHALL populate the agent block from that auth context.

#### Scenario: Write as agent principal
- **WHEN** the server appends an audit entry for a request authenticated as an agent-kind principal with a delegation
- **THEN** the stored entry carries the agent subject, the delegation ID, and the reason from that context.

### Requirement: Append with agent block

When a stored audit entry carries an agent block, the server SHALL cover agent subject, delegation ID, and reason in the chain hash.

#### Scenario: Verify chain with agent entry
- **WHEN** a stored entry carries an agent block
- **THEN** chain verification passes and the entry returns agent, delegation, and reason.

### Requirement: Canonical hash encoding

When the server hashes an entry with an agent block, the server SHALL encode agent, delegation, and reason as 4-byte big-endian length plus UTF-8 bytes in that fixed order.

#### Scenario: Encode block in fixed order
- **WHEN** the server hashes an entry with an agent block
- **THEN** the hash input equals length-prefixed agent, delegation, and reason in that order and an absent block contributes zero bytes.

### Requirement: Keep old hash without block

When a stored audit entry carries no agent block, the server SHALL verify it with the current hash.

#### Scenario: Verify old entry
- **WHEN** a stored entry carries no agent block
- **THEN** chain verification passes with the current hash.

### Requirement: Reject bad agent identity

If the agent subject exceeds 256 characters per entry or the delegation ID breaks the fixed grammar, then the server SHALL reject the write with the NEW error code audit.agent-invalid.

#### Scenario: Reject malformed agent subject
- **WHEN** the server populates an agent block with a malformed agent subject or a delegation ID outside the fixed grammar
- **THEN** the server rejects the write with the NEW error code audit.agent-invalid.

### Requirement: Bound reason length

If an audit agent block carries a reason longer than 1024 characters per entry, then the server SHALL reject the write with the NEW error code audit.agent-invalid.

#### Scenario: Reject overlong reason
- **WHEN** the server populates a reason longer than 1024 characters
- **THEN** the server rejects the write with the NEW error code audit.agent-invalid.

### Requirement: Atomic principal audit write

When the chain hash fails during a principal create, the server SHALL store neither the principal row nor its audit entry.

#### Scenario: Fail hash on principal create
- **WHEN** the chain hash fails during a principal create
- **THEN** neither the principal row nor its audit entry is stored.
