## ADDED Requirements

### Requirement: AppendAuditEntry short transaction

When the server appends an audit entry for a namespace, the server SHALL run the LastAuditEntry and InsertAuditEntry pair in one short transaction after the read.

#### Scenario: AppendAuditEntry concurrent appends stay linked
- **WHEN** two AppendAuditEntry calls for one namespace race
- **THEN** both entries commit with each entry linked to its predecessor and no fork

### Requirement: AppendAuditEntry stale fence refuses

If the fence of an audit append is stale, then the server SHALL refuse the append with no new entry.

#### Scenario: AppendAuditEntry stale failover version
- **WHEN** the fence carries a stale failover version
- **THEN** the server refuses the append with no new entry

### Requirement: AppendAuditEntry first entry

When a namespace has no audit entry, the server SHALL insert the first entry without a predecessor.

#### Scenario: AppendAuditEntry first entry of a new namespace
- **WHEN** a namespace has no audit entry
- **THEN** the server inserts the first entry without a predecessor
