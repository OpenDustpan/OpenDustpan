## ADDED Requirements

### Requirement: rewrap-schedule

When the schedule namespace-key.rewrap fires, the server SHALL run Rewrap for each namespace key version only on its primary installation.

#### Scenario: daily rewrap on primary
- **WHEN** the schedule namespace-key.rewrap fires for a namespace on its primary installation
- **THEN** each version carries current wrapper IDs and an audit entry of kind change records the Rewrap.

#### Scenario: replica skips rewrap
- **WHEN** the schedule fires on a replica installation
- **THEN** the replica makes no write and answers ErrNotHome for a forwarded write.

### Requirement: rotate

When a namespace administrator rotates the namespace key, the server SHALL make a new active version with RotateNamespaceKey and keep old versions readable while a backup record names them.

#### Scenario: RotateNamespaceKey keeps content
- **WHEN** a namespace administrator rotates the namespace key through RotateNamespaceKey
- **THEN** new reads use the new version and old item key blobs still open.

### Requirement: re-encrypt

When a namespace administrator starts re-encryption, the server SHALL run it with StartReEncrypt as an audited durable operation that re-seals wraps only.

#### Scenario: StartReEncrypt audited durable run
- **WHEN** a namespace administrator starts re-encryption with StartReEncrypt and a fresh MFA step
- **THEN** the server runs the work as a durable operation and writes audit entries of kind change for start and end.

### Requirement: old-wrap-read

When the server opens an item key blob sealed under an old namespace key version after a rewrap, the server SHALL return its plain text.

#### Scenario: old item key opens
- **WHEN** the server opens an item key blob sealed under an old namespace key version after a rewrap
- **THEN** the server returns its plain text with no re-seal of the caller data.

#### Scenario: damaged blob fails closed
- **WHEN** no stored namespace key version opens an item key blob
- **THEN** the server answers 500 with no content after all versions fail.

### Requirement: refusal

When a caller without namespace:manage-key starts rotation or re-encryption, the server SHALL answer 403.

#### Scenario: no grant refused
- **WHEN** a caller without namespace:manage-key starts rotation or re-encryption
- **THEN** the NEW route answers 403 and writes no key change.

#### Scenario: stale MFA refused
- **WHEN** a caller starts re-encryption with a stale MFA step
- **THEN** the NEW route answers 403 and names the fresh MFA step.

### Requirement: kms-failure

When the KMS is unavailable during Rewrap, the server SHALL answer 503 and keep the old wrap readable.

#### Scenario: KMS outage keeps old wrap
- **WHEN** the KMS is unavailable during Rewrap
- **THEN** the NEW route answers 503 with Retry-After and the old wrap stays readable.

#### Scenario: revoked key refused
- **WHEN** the KMS answers revoked or the probe fails at Rewrap
- **THEN** the NEW route answers 403 and writes an audit entry of kind change.

### Requirement: rotation-schedule

While automatic rotation is on, the server SHALL rotate the namespace key every 365 days by default within 90 to 2560 days.

#### Scenario: yearly rotation default
- **WHEN** 365 days pass with no rotation
- **THEN** the server rotates the namespace key and shows the effective value in the API.
