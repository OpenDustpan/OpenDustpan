## ADDED Requirements

### Requirement: StagePortable reuses checked template

When the server runs StagePortable, the server SHALL copy a checked migrated empty-schema template whose recorded versions and hashes match the binary.

#### Scenario: Second StagePortable call reuses template
- **WHEN** two StagePortable calls run in one process
- **THEN** the second StagePortable call starts without running the full migration chain and its recorded versions and hashes match the binary.

#### Scenario: PostgreSQL mismatch is refused
- **WHEN** PostgreSQL recorded versions or hashes do not match the binary
- **THEN** the server refuses the stage with backup.corrupt and stages no rows.

### Requirement: buildPortableStaging refuses changed template

When the copied bytes versions or hashes do not match the binary, the server SHALL refuse the buildPortableStaging call with backup.corrupt.

#### Scenario: Changed template is refused
- **WHEN** the copied bytes versions or hashes do not match the binary
- **THEN** the server refuses the stage with backup.corrupt and writes no staged rows.

### Requirement: Restore drops SQL staging lock file

When a SQLite restore ends, the server SHALL remove its per-restore staging lock file.

#### Scenario: Restore end removes lock file
- **WHEN** a SQLite restore of one restore id ends by success, refusal, failure, or check-only run
- **THEN** no file with the restore stage lock suffix of that restore id stays next to the database.

### Requirement: Restore drops empty blob staging tree

When a restore ends, the server SHALL remove its empty blob restore-tmp directory tree.

#### Scenario: Empty restore-tmp tree is removed
- **WHEN** a restore with a blob restore-tmp tree ends
- **THEN** the empty restore-tmp tree of that restore id is gone.

#### Scenario: Non-empty or live tree is kept
- **WHEN** the restore-tmp tree holds files or belongs to a running restore
- **THEN** the server keeps the tree and reports the restore result without deleting foreign files.

### Requirement: Backup check sees copied backup

When a backup directory is present with its manifest, the server SHALL report it as found by backup check.

#### Scenario: Copied backup is found
- **WHEN** a person copies a backup directory and its named blobs to a second installation
- **THEN** backup check reports the backup as present without a registry row from the source installation.

#### Scenario: Missing backup is not found
- **WHEN** no backup directory and no registry row name the backup
- **THEN** backup check answers 404 with backup not found.

#### Scenario: Corrupt manifest is refused
- **WHEN** the copied backup manifest is missing or corrupt
- **THEN** backup check refuses the backup with backup.corrupt.
