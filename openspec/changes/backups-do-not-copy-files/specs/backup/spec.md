## ADDED Requirements

### Requirement: copy-registry-release-files

When a namespace backup includes a registry release, the server SHALL copy each release file named by its SHA-256 content key into the backup.

#### Scenario: Backup names each release file
- **WHEN** a namespace backup includes a registry release with files
- **THEN** the backup holds one entry for each unique release file with its SHA-256 content key.

#### Scenario: Shared content key stored once
- **WHEN** two releases name the same SHA-256 content key
- **THEN** the backup holds one entry and one stored copy for the shared SHA-256 key.

### Requirement: copy-published-only

Where registry storage is backed up, the server SHALL copy only files named by published releases.

#### Scenario: Staged upload skipped
- **WHEN** a namespace holds a staged upload with no published release
- **THEN** the backup holds no entry for the staged file and the restore recreates no staged file.

### Requirement: backup-collector-inputs

When the registry file collector starts with an empty namespace ID or reads an empty file hash, the server SHALL stop with an error that names the bad input.

#### Scenario: Empty namespace ID rejected
- **WHEN** the collector starts with an empty namespace ID
- **THEN** the collector returns an error that names the namespace ID.

#### Scenario: Empty hash rejected
- **WHEN** a release row holds an empty file hash
- **THEN** the backup stops with an error that names the release and version.

### Requirement: backup-snapshot

When a namespace backup runs, the server SHALL read registry rows and the file list from one snapshot of the namespace.

#### Scenario: Stable row and file set
- **WHEN** a release is published while a namespace backup runs
- **THEN** the backup holds the rows and the files from the same point in time.

### Requirement: backup-verify-hash

When a backup reads a registry release file, the server SHALL stop the backup if the bytes do not match the SHA-256 content key.

#### Scenario: Corrupt source refused at backup
- **WHEN** a release file in the blob store does not match its SHA-256 content key
- **THEN** the backup stops with an error that names the release, the version, and the SHA-256 key.

### Requirement: backup-manifest-shape

When a backup reads a release row with a file hash outside 64 lowercase hex characters, the server SHALL stop the backup with an error that names the release and version.

#### Scenario: Hostile key rejected
- **WHEN** a release row holds a file hash with path separators or characters outside lowercase hex
- **THEN** the backup stops with an error that names the release and version.

### Requirement: backup-stop-unreadable-file

When a backup cannot read a registry release file from the blob store, the server SHALL stop the backup with an error that names the release and version.

#### Scenario: Unreadable file stops backup
- **WHEN** a registry release file cannot be read from the blob store
- **THEN** the backup stops with an error that names the release and version.

### Requirement: backup-report-size

When a namespace backup copies registry release files, the server SHALL report the count of files and the total bytes in the backup result.

#### Scenario: Size reported
- **WHEN** a namespace backup copies registry release files
- **THEN** the backup result reports the count of registry files and the total bytes copied.

### Requirement: check-registry-files-on-restore

When a restore reads a namespace backup that names a registry file, the server SHALL refuse the restore if that file is missing.

#### Scenario: Missing file fails restore
- **WHEN** a backup names a registry file that the backup does not hold
- **THEN** the restore stops with an error that names the missing SHA-256 key and writes no registry file.

### Requirement: restore-check-before-write

When a restore reads a namespace backup with registry files, the server SHALL check all named files before writing any registry file.

#### Scenario: Partial manifest writes nothing
- **WHEN** a backup manifest is incomplete or corrupt
- **THEN** the target store holds no new registry file from the backup.

### Requirement: restore-verify-hash

When a restore writes a registry file, the server SHALL refuse the file if its bytes do not match its SHA-256 content key.

#### Scenario: Corrupt bytes refused
- **WHEN** a backup file does not match its SHA-256 content key
- **THEN** the restore stops with an error that names the SHA-256 key and writes no registry file.

#### Scenario: Valid files land under target ID
- **WHEN** a backup with registry files is restored to another installation
- **THEN** the target blob store holds each release file under namespaces/<namespace ID>/registry/sha256/ keyed by the target namespace ID.

### Requirement: restore-idempotent-retry

When a registry file restore stops midway, the server SHALL remove partial files so a retry with the same backup completes.

#### Scenario: Midway failure leaves no partial files
- **WHEN** a restore stops after writing part of the registry files
- **THEN** the target store holds no partial registry file and a retry with the same backup completes the copy.

#### Scenario: Retry over existing keys completes
- **WHEN** a restore runs twice onto a target that already holds part of the SHA-256 keys
- **THEN** the restore keeps the matching keys and writes the missing keys.

### Requirement: restore-legacy-without-entries

When a restore reads a backup with registry rows and no registry file manifest section, the server SHALL warn and restore the rows without files.

#### Scenario: Old backup warns and proceeds
- **WHEN** a backup holds registry rows with no registry file manifest section
- **THEN** the restore warns that registry files are absent, restores registry rows and signing keys without files, and names the backup command that makes a complete backup.

### Requirement: restore-authorized-audited

When a principal restores a namespace backup with registry files, the server SHALL check the restore action and write an audit entry for the restore.

#### Scenario: Restore checked and recorded
- **WHEN** a principal restores a namespace backup with registry files
- **THEN** the server allows the call only with the restore action on the installation and writes an audit entry that names the namespace and the count of registry files.
