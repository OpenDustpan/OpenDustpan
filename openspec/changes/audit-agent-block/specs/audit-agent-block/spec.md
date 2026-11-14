## ADDED Requirements

### Requirement: No block for an entry without agent data

When all six agent fields of an entry are empty and breakGlass is false, the server SHALL compute the hash of the entry with ComputeHash using the same bytes as before the block existed.

#### Scenario: Old chain
- **WHEN** the chain check runs on a chain stored before this change
- **THEN** every entry passes

### Requirement: Block in the hash

When any agent field of an entry is set, the server SHALL include the agent/1 block with the six fields in the hash that ComputeHash returns.

#### Scenario: One field differs
- **WHEN** ComputeHash runs on two entries that differ only in the tool field
- **THEN** their hashes differ

### Requirement: Fixed field order

When the agent/1 block is written, the server SHALL encode the fields in the order agentRun, agentSession, tool, argsDigest, breakGlass as true or false, secretStage, each length-prefixed.

#### Scenario: Order
- **WHEN** two entries swap the values of agentRun and agentSession
- **THEN** their hashes differ

#### Scenario: Empty field inside the block
- **WHEN** an entry sets only agentRun
- **THEN** the block still holds all six fields, each length-prefixed

### Requirement: Job entry fields

When a job entry of an agent is written, the server SHALL leave tool, argsDigest and secretStage empty in the agent block.

#### Scenario: Job start
- **WHEN** an agent starts a job through the channel agent
- **THEN** agentRun is the run ID and tool, argsDigest and secretStage are empty

### Requirement: Stored columns

The server SHALL store the six agent fields of an entry in columns of the audit table of each dialect.

#### Scenario: Round trip
- **WHEN** an entry with the six fields is written and read
- **THEN** the read entry holds the same six values

### Requirement: API field

When an entry has agent data, the server SHALL return the agent block in the API entry.

#### Scenario: Agent job entry
- **WHEN** a job started by an agent writes an entry and the administrator reads it
- **THEN** the entry shows agentRun, agentSession and breakGlass in the agent block

### Requirement: Invalid field refused

If an agent field is not valid, then the server SHALL refuse the write of the entry.

#### Scenario: Control character
- **WHEN** a field holds a control character
- **THEN** the write fails and nothing is stored
