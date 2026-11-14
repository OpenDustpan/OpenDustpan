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

### Requirement: Read legacy change-key entries

Where an old start entry carries the agent change key and no block, the server SHALL return the legacy agent value on read and verify its chain.

#### Scenario: Legacy start entry
- **WHEN** an old start entry carries the agent change key and no block
- **THEN** the read returns the legacy agent value and the chain verifies

### Requirement: Block wins over legacy key

When an entry carries both the agent block and the legacy agent change key, the server SHALL return the agent ref from the block.

#### Scenario: Both shapes present
- **WHEN** an entry carries both the agent block and the legacy agent change key
- **THEN** the read returns the agent ref from the block

#### Scenario: Tampered block with both shapes
- **WHEN** one byte of the block changes on an entry that also carries the legacy key
- **THEN** the chain check refuses the chain

### Requirement: Keep agent approval refused

When an agent attempts an approval, a confirmation, or a break-glass, the server SHALL refuse the attempt.

#### Scenario: Agent approval attempt
- **WHEN** an agent attempts an approval, a confirmation, or a break-glass
- **THEN** the server refuses the approval and writes no approval entry

### Requirement: Answer a missing entry

If an entry id does not exist in the namespace scope, then the server SHALL answer 404 with the error code audit.entry-not-found.

#### Scenario: Unknown entry id
- **WHEN** a caller reads an entry id that does not exist
- **THEN** the server answers 404 with audit.entry-not-found

### Requirement: Answer an unreadable entry

If an entry cannot be read during a chain check, then the server SHALL answer with the error code audit.entry-read-unavailable.

#### Scenario: Unreadable entry in chain check
- **WHEN** the chain check meets an entry that the store cannot read
- **THEN** the server answers with audit.entry-read-unavailable and the check changes nothing
