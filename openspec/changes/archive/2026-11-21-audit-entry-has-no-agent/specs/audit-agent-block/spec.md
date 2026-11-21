## ADDED Requirements

### Requirement: Record job start agent in block

When a job starts with an agent ref, the server SHALL write the agent ref in the agent block of the start entry with no Agent field on audit.Entry.

#### Scenario: Agent start entry
- **WHEN** a job starts with an agent ref
- **THEN** the start entry holds the agent ref in the agent block, holds no Agent field, and holds no agent change key

### Requirement: Omit block when no agent acts

When a job changes with no agent ref, the server SHALL write the audit entry with no agent block.

#### Scenario: Human-only entry
- **WHEN** a job changes with no agent ref
- **THEN** the entry holds no agent block and verifies with the unchanged hash

### Requirement: Keep hash stable without block

While an entry holds no agent block, the server SHALL compute its hash with the unchanged encoding.

#### Scenario: Hash stability
- **WHEN** an entry holds no agent block
- **THEN** the hash equals the hash computed before this change

### Requirement: Cover block in verification

When an entry holds an agent block, the server SHALL cover the block bytes in chain verification.

#### Scenario: Tampered block
- **WHEN** one byte of the agent block changes
- **THEN** the chain check refuses the chain

### Requirement: Keep agent approval refused

When an agent attempts an approval, a confirmation, or a break-glass, the server SHALL refuse the attempt.

#### Scenario: Agent approval attempt
- **WHEN** an agent attempts an approval, a confirmation, or a break-glass
- **THEN** the server refuses the approval and writes no approval entry

