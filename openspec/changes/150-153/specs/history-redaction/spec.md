## ADDED Requirements

### Requirement: redact-item

When a caller with NEW namespace:redact-history redacts one item with a reason from the allowlist secret-exposure, personal-data, other, the server SHALL destroy its content and keep its metadata.

#### Scenario: redact keeps metadata
- **WHEN** a namespace administrator redacts one item with reason secret-exposure
- **THEN** content is null, metadata remains and a Redact audit entry is written

#### Scenario: second redact
- **WHEN** a namespace administrator redacts an item that is already redacted
- **THEN** the server returns 409 history.already-redacted

#### Scenario: bad reason
- **WHEN** a namespace administrator redacts with a reason outside the allowlist
- **THEN** the server returns 400 history.bad-reason

### Requirement: redact-order

When Redact runs on one item, the server SHALL delete each blob copy of that item before it marks the row redacted.

#### Scenario: blob first
- **WHEN** Redact runs on one item
- **THEN** the service deletes the blob copies before it marks the row

### Requirement: repair-pass

When the retention tick runs, the server SHALL delete orphan blob copies of redacted items.

#### Scenario: orphans removed
- **WHEN** the tick runs after a crash between blob delete and row update
- **THEN** the sweep deletes the copies and writes one audit entry per copy

### Requirement: retention-expiry

When the job artifact schedule runs, the server SHALL delete job artifacts older than 30 days per namespace.

#### Scenario: sweep deletes
- **WHEN** the job artifact schedule runs on a namespace
- **THEN** artifacts older than 30 days are deleted and metadata remains

#### Scenario: sweep repeats
- **WHEN** the schedule runs twice with no new artifacts
- **THEN** the second run deletes nothing and succeeds

### Requirement: retention-audit

When the job artifact schedule deletes one artifact, the server SHALL write one audit entry for that expiry.

#### Scenario: expiry writes audit
- **WHEN** the job artifact schedule deletes one artifact
- **THEN** the server writes one audit entry with system actor, unit reference, artifact id and reason retention-expired

### Requirement: safe-delete

When a caller with the existing restore grant calls Restore on a deleted unit within 30 days, the server SHALL restore it from its tombstone.

#### Scenario: restore in window
- **WHEN** an administrator restores a unit within 30 days
- **THEN** the unit is present again with its history through Restore

#### Scenario: restore after window
- **WHEN** an administrator restores a unit after 30 days
- **THEN** the server returns 410 unit.expired

### Requirement: history-unavailable

When the store or blob read fails during a history read, the server SHALL return 5xx history.unavailable.

#### Scenario: fail closed
- **WHEN** the blob read fails during a history read
- **THEN** the server returns 5xx history.unavailable with an audit entry and no partial content
