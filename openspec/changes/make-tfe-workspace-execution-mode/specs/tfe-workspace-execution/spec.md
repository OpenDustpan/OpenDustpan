## ADDED Requirements

### Requirement: Mode PATCH

When a caller with the tool-neutral manage action sends a PATCH with execution-mode local or agent, the server SHALL store the mode of the workspace via setExecutionMode.

#### Scenario: Agent stored via setExecutionMode
- **WHEN** a caller with the manage action PATCHes execution-mode agent with a pool of the namespace
- **THEN** the tool-neutral read shows the same mode and pool

#### Scenario: Remote refused
- **WHEN** the PATCH carries execution-mode remote
- **THEN** the server answers a JSON:API error and stores nothing

#### Scenario: Mode local with pool change
- **WHEN** the PATCH carries mode local with a pool of the namespace
- **THEN** the server stores mode local and the new pool ID

### Requirement: Pool PATCH

When a caller with the tool-neutral manage action sends a PATCH with the agent-pool relation, the server SHALL store the stable ID of a unit-usable runner pool of the same namespace via setPool.

#### Scenario: Pool stored via setPool
- **WHEN** the relation names a unit-usable pool of the namespace
- **THEN** the tool-neutral read shows the same stable pool ID

#### Scenario: Foreign pool refused
- **WHEN** the pool is unknown or in another namespace
- **THEN** the server answers a JSON:API error and stores nothing

#### Scenario: Operator pool refused
- **WHEN** the pool is an operator pool or a sweep-purpose pool
- **THEN** the server answers a JSON:API error and stores nothing

#### Scenario: Null clears
- **WHEN** the relation is null
- **THEN** the unit has no pool

### Requirement: Same authorization

If the caller lacks the tool-neutral manage action, then the server SHALL refuse the PATCH.

#### Scenario: No action
- **WHEN** a caller without the manage action sends the PATCH
- **THEN** the server answers 403 and stores nothing

#### Scenario: No credential
- **WHEN** a caller sends no credential
- **THEN** the server answers 401

#### Scenario: Missing workspace
- **WHEN** the PATCH names a workspace that does not exist
- **THEN** the server answers 404 with no existence leak beyond the code

### Requirement: Atomic write

When a PATCH carries a mode and a pool, the server SHALL write both in one transaction.

#### Scenario: Concurrent PATCH
- **WHEN** two callers PATCH at the same time
- **THEN** the stored mode and pool match one whole winner

#### Scenario: Agent with no pool
- **WHEN** the PATCH sets agent and the unit has no pool
- **THEN** the server answers a JSON:API error and stores nothing

#### Scenario: Pool only PATCH
- **WHEN** the PATCH carries only the agent-pool relation
- **THEN** the stored pool changes and the mode does not change

#### Scenario: Namespace without pool
- **WHEN** the PATCH sets agent in a namespace with no runner pool
- **THEN** the server answers a JSON:API error and stores nothing

### Requirement: Audit record

When a PATCH changes the mode or the pool, the server SHALL write one audit entry.

#### Scenario: Change audited
- **WHEN** a PATCH changes the mode
- **THEN** the audit log holds one entry with the principal and the unit

#### Scenario: Default visible
- **WHEN** no PATCH changed the mode
- **THEN** a read shows execution-mode local

#### Scenario: Ignored attributes
- **WHEN** the PATCH carries other provider attributes with execution-mode
- **THEN** the server stores only mode and pool and ignores the rest
