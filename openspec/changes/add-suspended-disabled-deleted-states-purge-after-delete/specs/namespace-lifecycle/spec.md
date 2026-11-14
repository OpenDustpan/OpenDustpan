## ADDED Requirements

### Requirement: batched purge after delete

When a namespace is in state deleted for at least 30 days, the server SHALL purge it with PurgeDeletedNamespaces in a bounded batch.

#### Scenario: Purge eligible deleted namespace
- **WHEN** the purge task selects a namespace in state deleted with delete time at least 30 days past
- **THEN** the server removes the namespace and its owned rows including units, state, history, locks, secrets, blobs, grants and audit records.

#### Scenario: Exact 30 day boundary
- **WHEN** delete time equals exactly 30 days past
- **THEN** the server purges the namespace in the next batch.

#### Scenario: Backlog over one batch
- **WHEN** more than 100 namespaces are eligible
- **THEN** the server purges the oldest 100 and leaves the rest for the next run.

### Requirement: keep ineligible namespace

While a namespace is in state deleted for less than 30 days, the server SHALL retain it.

#### Scenario: Keep recent deleted namespace
- **WHEN** the delete time is less than 30 days past
- **THEN** the server leaves the namespace in state deleted.

### Requirement: recheck before purge commit

When the purge task selects a deleted namespace, the server SHALL recheck state deleted and delete time inside the purge transaction with PurgeNextBatch.

#### Scenario: Restore wins over purge
- **WHEN** a restore changes the namespace before the purge transaction commits
- **THEN** the server skips the purge of that namespace in that transaction.

### Requirement: purge audit record

When the server purges a deleted namespace, the server SHALL write one audit entry that names the namespace and the purge reason.

#### Scenario: Audit each purge
- **WHEN** the server purges a deleted namespace
- **THEN** the server writes one audit entry that names the namespace, the delete time and the purge reason.
