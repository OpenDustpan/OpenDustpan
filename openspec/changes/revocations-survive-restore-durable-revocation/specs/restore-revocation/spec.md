## ADDED Requirements

### Requirement: ReplayRevocations

When a same-installation restore applies a backup with taken-at time T0, the server SHALL re-apply each revocation tombstone with at_ms later than T0 through ReplayRevocations.

#### Scenario: ReplayRevocations keeps a revocation after backup
- **WHEN** a token revoked at 10:00 is restored from a 09:00 backup of the same installation
- **THEN** the token is refused and one audit entry per applied target exists

#### Scenario: ReplayRevocations skips an early tombstone
- **WHEN** a tombstone has at_ms at or before T0
- **THEN** no re-apply occurs for that row

#### Scenario: ReplayRevocations refuses an unknown kind
- **WHEN** a tombstone carries an unknown kind
- **THEN** access stays denied and one audit entry exists

#### Scenario: ReplayRevocations rerun is idempotent
- **WHEN** ReplayRevocations runs again after a takeover
- **THEN** each target is applied one time with one audit entry per target

### Requirement: Live revocation writes tombstone

When a live path revokes or disables a session, token, grant, join token, principal or identity provider, the server SHALL write a tombstone in the same transaction.

#### Scenario: Token revoke
- **WHEN** a service token is revoked
- **THEN** a tombstone with the database-clock at_ms exists

#### Scenario: Grant revoke
- **WHEN** a grant is revoked
- **THEN** a tombstone exists

#### Scenario: Provider disable
- **WHEN** an identity provider is disabled
- **THEN** a tombstone exists

### Requirement: Cross-installation skip

When a restore applies a backup with a missing or different installation ID, the server SHALL skip replay.

#### Scenario: Foreign backup
- **WHEN** a foreign backup is restored
- **THEN** no tombstone is applied and one audit entry names the skip
