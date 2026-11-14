## ADDED Requirements

### Requirement: Restore access setting default

When the installation setting is off, the server SHALL refuse to replace the access tables on a restore by a caller without server:restore-access.

#### Scenario: Default off refusal
- **WHEN** the setting is off and a caller with only server:restore restores a backup with access tables
- **THEN** the server keeps the access tables unchanged and refuses the access-table part.

#### Scenario: Default off with access action
- **WHEN** the setting is off and a caller with server:restore-access restores a backup with access tables
- **THEN** the server replaces the access tables from the backup.

### Requirement: Setting change audit

When an allowed caller changes the setting with a fresh authentication step, the server SHALL write an audit entry with the old and the new value.

#### Scenario: Change recorded
- **WHEN** an allowed caller changes the setting with a fresh authentication step
- **THEN** the server writes an audit entry with the old and the new value.

#### Scenario: Change without fresh step refused
- **WHEN** a caller changes the setting without a fresh authentication step
- **THEN** the server refuses the change with mfa.fresh-step-required, keeps the stored value, and writes no setting-change entry.

#### Scenario: Change without action refused
- **WHEN** a caller without the gated actions changes the setting
- **THEN** the server refuses the change with 403, keeps the stored value, and writes no setting-change entry.

### Requirement: Restore with setting on

When the setting is on, the server SHALL let a caller with server:restore replace the access tables on restore.

#### Scenario: Widened restore
- **WHEN** the setting is on and a caller with server:restore restores a backup
- **THEN** the server replaces the access tables from the backup.

#### Scenario: Snapshot kept mid-restore
- **WHEN** the setting changes after a restore read its start value
- **THEN** the server finishes the restore on the start value and ignores the later change.

### Requirement: Widened restore keeps two paths

When a restore replaces the access tables, the server SHALL refuse a result with fewer than two working break-glass paths.

#### Scenario: Last path refused
- **WHEN** a widened restore leaves fewer than two working break-glass paths
- **THEN** the server refuses the restore with 409 and changes nothing.

#### Scenario: Concurrent disable serializes
- **WHEN** a break-glass disable commits while a widened restore applies in the same write transaction
- **THEN** one of the two writes refuses with 409 and the stored data keeps two working paths.

### Requirement: Invalid stored value fails closed

If the stored value of the setting is not true or false, then the server SHALL refuse to replace the access tables.

#### Scenario: Unknown value refused
- **WHEN** the stored value of the setting is not true or false at restore apply time
- **THEN** the server refuses the access-table part and changes nothing.

### Requirement: Effective value visible

When a permitted caller reads the installation settings, the server SHALL answer the stored value of the setting and its default.

#### Scenario: Read shows value
- **WHEN** a permitted caller reads the installation settings
- **THEN** the server answers the stored value and its default.

### Requirement: Widened state reported

When the setting is on, the server SHALL list the widened restore state in the admin access report.

#### Scenario: Report names widened restore
- **WHEN** the setting is on and a permitted caller reads the admin access report
- **THEN** the server lists the setting value and its last change.
