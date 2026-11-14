## ADDED Requirements

### Requirement: Record baseline hash

When the server makes a backup, the server SHALL record the SHA-256 of the baseline in the backup manifest.

#### Scenario: Manifest holds hash
- **WHEN** Server makes a backup.
- **THEN** Manifest holds the SHA-256 of the raw embedded baseline SQL bytes as 64 lowercase hex chars.

### Requirement: Refuse hash mismatch

When the manifest SHA-256 differs from the binary baseline SHA-256, the server SHALL refuse the restore before it changes anything.

#### Scenario: Wrong hash refused
- **WHEN** Manifest SHA-256 differs from the binary baseline SHA-256.
- **THEN** Restore refuses with `restore.baseline-mismatch` and changes no data.

#### Scenario: Wrong version refused first
- **WHEN** Manifest holds the correct hash but a wrong schema version or a wrong dialect.
- **THEN** Restore refuses at the dialect or version compare, which main already does, before the hash compare, and changes no data.

### Requirement: Refuse missing or malformed hash

When the manifest holds no baseline SHA-256 or a malformed SHA-256, the server SHALL refuse the restore before it changes anything.

#### Scenario: Old backup refused
- **WHEN** Manifest holds no baseline SHA-256.
- **THEN** Restore refuses with `restore.baseline-mismatch` and opens no table file.

#### Scenario: Malformed hash refused
- **WHEN** Manifest holds an empty, non-hex, or oversize hash or an oversize manifest.
- **THEN** Restore refuses with `restore.baseline-mismatch` and opens no table file.

#### Scenario: Denied caller refused
- **WHEN** Caller holds no restore grant.
- **THEN** Server denies the call with an authorization error and runs no baseline check.

### Requirement: Audit refusal

When the server refuses a restore for a baseline SHA-256 mismatch, the server SHALL write an audit entry and change no data.

#### Scenario: Mismatch refusal audited
- **WHEN** Restore refuses for a baseline SHA-256 mismatch.
- **THEN** Server writes an audit entry through the checked append port without hash bytes and a retry changes no data, and a failed audit write keeps the restore refused.

#### Scenario: Missing hash refusal audited
- **WHEN** Restore refuses for a missing or malformed baseline SHA-256.
- **THEN** Server writes an audit entry through the checked append port without hash bytes and opens no table file.
