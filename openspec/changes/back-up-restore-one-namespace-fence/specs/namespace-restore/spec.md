## ADDED Requirements

### Requirement: StartNamespaceRestore

When a caller restores the NEW `POST /api/dustpan/v1/namespaces/{namespace}/restore` with a namespace-scope backup, the server SHALL replace rows and blobs of that namespace with StartNamespaceRestore.

#### Scenario: Neighbor keeps changes
- **WHEN** operator backs up A, changes A and B, then restores A with StartNamespaceRestore
- **THEN** namespace A matches the backup and namespace B keeps changes made after the backup

### Requirement: StartNamespaceRestore safety backup

When a namespace restore starts, the server SHALL take a safety backup of the target namespace with StartNamespaceRestore and keep wrapped keys.

#### Scenario: Undo with safety backup
- **WHEN** a namespace restore starts
- **THEN** the restore result names the safety backup ID that restores the prior state

### Requirement: ApplyNamespaceRestore fenced writer

While a namespace restore applies, the server SHALL carry the bumped namespace fence on each write with ApplyNamespaceRestore and refuse a stale fence.

#### Scenario: Stale writer refused
- **WHEN** a write carries an old failover version during restore with ApplyNamespaceRestore
- **THEN** the write is refused and the audit entry names the fence mismatch

### Requirement: StartNamespaceRestore checks before apply

When a caller starts a namespace restore with StartNamespaceRestore, the server SHALL check manifest scope, blob presence, table coverage, and each namespace key version before any live write.

#### Scenario: Wrong scope refused
- **WHEN** the manifest scope is not namespace or a named blob is missing
- **THEN** the server refuses with the NEW error code `namespace-restore.bad-backup` before any live write

### Requirement: Tombstone replay

When a namespace restore applies, the server SHALL replay tombstones and shredded item-key state before the staged swap.

#### Scenario: Redacted content stays gone
- **WHEN** the backup holds redaction ledger entries and tombstones
- **THEN** the restored namespace has no redacted content and no purged unit content

### Requirement: Grants and home principals

When a namespace restore applies, the server SHALL replace grants on the namespace and principals whose home is the namespace.

#### Scenario: Grants restored
- **WHEN** a namespace restore applies
- **THEN** grants on the namespace and home principals match the backup and installation principals stay unchanged

### Requirement: Restore under another key

When a namespace restore targets a namespace under another key, the server SHALL rewrap keys under the target key.

#### Scenario: Rewrap under target key
- **WHEN** the backup was wrapped under another key
- **THEN** sealed content opens under the target namespace key

### Requirement: Authorization

When a caller lacks the NEW action `namespace:restore` on the namespace, the server SHALL refuse the restore with 403.

#### Scenario: Missing grant refused
- **WHEN** the caller lacks the NEW action `namespace:restore` on the namespace
- **THEN** the server refuses with 403 and no restore record is made

### Requirement: Concurrent isolation

While a namespace restore is in checking or applying, the server SHALL refuse a second restore on the same namespace with 409.

#### Scenario: Second restore refused
- **WHEN** a second restore starts while one restore is in checking or applying
- **THEN** the server refuses with the NEW error code `namespace-restore.conflict`

### Requirement: Audit record and poll

When a namespace restore starts and ends, the server SHALL answer 202 with a restore record on the NEW `GET /api/dustpan/v1/namespaces/{namespace}/restores/{id}`.

#### Scenario: Restore observed
- **WHEN** a namespace restore starts and ends
- **THEN** the server answers 202 with a restore record and writes start and end audit entries with backup ID and manifest hash

### Requirement: Backup source and scope

When a namespace restore starts, the server SHALL read the backup by backupId from the configured target.

#### Scenario: Cross namespace refused
- **WHEN** the backup names namespace A and the target is namespace B
- **THEN** the server refuses with the NEW error code `namespace-restore.bad-backup` before any live write
