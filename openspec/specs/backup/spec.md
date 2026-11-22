# backup Specification

## Purpose
This capability covers registry files in backups: each published release file is copied once under its SHA-256 content key, staged uploads are skipped, and a missing file stops the backup with a named error. It stops at file copies; the rest of backup and restore stays outside.

## Requirements

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

### Requirement: backup-missing-file

When a backup finds a published release that names a SHA-256 key with no file, the server SHALL stop the backup with an error that names the release and version.

#### Scenario: Missing key stops backup
- **WHEN** a published release names a SHA-256 key with no file in the blob store
- **THEN** the backup stops with an error that names the release, the version, and the missing SHA-256 key.

### Requirement: backup-stop-unreadable-file

When a backup cannot read a registry release file from the blob store, the server SHALL stop the backup with an error that names the release and version.

#### Scenario: Unreadable file stops backup
- **WHEN** a registry release file cannot be read from the blob store
- **THEN** the backup stops with an error that names the release and version.

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

#### Scenario: Stable set on SQLite
- **WHEN** a release is published while a namespace backup runs on SQLite
- **THEN** the backup holds the rows and the files from the same point in time.

#### Scenario: Stable set on PostgreSQL
- **WHEN** a release is published while a namespace backup runs on PostgreSQL
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

### Requirement: registry-file-entry-shape

When a namespace backup holds registry files, the server SHALL write one entry per unique file with its SHA-256 key, size, release, version, and a section version field.

#### Scenario: Entry shape fixed
- **WHEN** a namespace backup holds registry files
- **THEN** each entry holds the 64 lowercase hex SHA-256 key, the size in bytes, and the release name and version.

#### Scenario: Old reader reads new backup
- **WHEN** a reader before this change reads a backup with the registry file section
- **THEN** the old reader ignores the registry file section by its version field and warns that files are absent.

### Requirement: backup-report-size

When a namespace backup copies registry release files, the server SHALL report the count of files and the total bytes in the backup result.

#### Scenario: Size reported
- **WHEN** a namespace backup copies registry release files
- **THEN** the backup result reports the count of registry files and the total bytes copied.

### Requirement: backup-authorized-audited

When a principal creates an installation backup that holds registry files, the server SHALL check the server:backup action and write an audit entry for the backup.

#### Scenario: Backup checked and recorded
- **WHEN** a principal creates an installation backup that holds registry files
- **THEN** the server allows the call only with the server:backup action on the installation and writes an audit entry for the backup.

### Requirement: check-registry-files-on-restore

When a restore reads a namespace backup that names a registry file, the server SHALL refuse the restore if that file is missing.

#### Scenario: Missing file fails restore
- **WHEN** a backup names a registry file that the backup does not hold
- **THEN** the restore stops with an error that names the missing SHA-256 key and makes no registry file visible.

### Requirement: restore-manifest-shape

When a restore reads a backup manifest with a registry file key outside 64 lowercase hex characters, the server SHALL refuse the restore before any registry file write.

#### Scenario: Hostile manifest rejected
- **WHEN** a backup manifest holds a registry file key outside 64 lowercase hex characters
- **THEN** the restore stops with an error that names the bad key and makes no registry file visible.

### Requirement: restore-check-before-write

When a restore reads a namespace backup with registry files, the server SHALL check all named files before making any registry file visible.

#### Scenario: Partial manifest writes nothing
- **WHEN** a backup manifest is incomplete or corrupt
- **THEN** the target store makes no new registry file visible from the backup.

### Requirement: restore-verify-hash

When a restore writes a registry file, the server SHALL refuse the file if its bytes do not match its SHA-256 content key.

#### Scenario: Corrupt bytes refused
- **WHEN** a backup file does not match its SHA-256 content key
- **THEN** the restore stops with an error that names the SHA-256 key and makes no registry file visible.

#### Scenario: Valid files land under target ID
- **WHEN** a backup with registry files is restored to another installation
- **THEN** the target blob store holds each release file under namespaces/<namespace ID>/registry/sha256/ keyed by the target namespace ID.

#### Scenario: Pre-existing key hash-checked
- **WHEN** the target store already holds different bytes under a SHA-256 key of the backup
- **THEN** the restore stops with an error that names the SHA-256 key and makes no registry file visible.

### Requirement: restore-idempotent-retry

When a registry file restore stops midway, the server SHALL remove only partial files this restore staged so a retry with the same backup completes.

#### Scenario: Midway failure leaves no partial files
- **WHEN** a restore stops after staging part of the registry files
- **THEN** the target store holds no partial registry file and a retry with the same backup completes the copy.

#### Scenario: Retry over existing keys completes
- **WHEN** a restore runs twice onto a target that already holds part of the SHA-256 keys
- **THEN** the restore keeps the matching keys and writes the missing keys.

### Requirement: restore-warn-channel

When a restore completes from a backup without registry file entries, the server SHALL return the file warning in the restore result and the CLI output.

#### Scenario: Warning visible in result
- **WHEN** a restore completes from a backup without registry file entries
- **THEN** the restore result holds a warning field that names the absent registry files and the backup command that makes a complete backup.

### Requirement: restore-authorized-audited

When a principal restores an installation backup that holds registry files, the server SHALL check the server:restore action and write an audit entry for the restore.

#### Scenario: Restore checked and recorded
- **WHEN** a principal restores an installation backup that holds registry files
- **THEN** the server allows the call only with the server:restore action on the installation and writes an audit entry for the restore.and the count of registry files.

### Requirement: Attempt-scoped target writes

The server SHALL write each backup attempt only under `backups/<backup id>/<attempt>/`.

#### Scenario: Stale delete after survivor finish
- **WHEN** a stale holder issues a late delete after the survivor committed finish
- **THEN** the survivor files remain readable and Check reports complete

### Requirement: Manifest names the attempt

When a backup attempt writes its manifest, the server SHALL record the claim fencing token as the attempt in the backups row and in the manifest.

#### Scenario: Attempt mismatch refuses finish
- **WHEN** the manifest attempt differs from the claim fencing token
- **THEN** the finish step fails without marking the backup succeeded

### Requirement: Fenced manifest write

When a backup attempt writes state, the server SHALL refuse the write unless the caller still holds the claim fencing token.

#### Scenario: Stale manifest refused
- **WHEN** a stale holder tries to write the manifest with an old fencing token
- **THEN** the manifest write fails and the live attempt is unchanged

### Requirement: Finish verification

When the finish step runs, the server SHALL check sizes and blob copies of the attempt prefix with Check before the fenced finish write.

#### Scenario: Truncated file fails finish
- **WHEN** a listed file of the attempt prefix is missing or its size differs from the manifest
- **THEN** the finish step fails with `backup.corrupt` and the backup does not read succeeded

### Requirement: Fenced finish commit

When the finish step commits, the server SHALL check the live claim and compare the manifest attempt with the attempt that the backups row records, in the same transaction. The finish step SHALL take the attempt from the backups row, not from the fencing token of its own claim.

#### Scenario: Stale finish refused
- **WHEN** the claim fencing token moved before the finish commit
- **THEN** the commit is refused and the backup stays running

#### Scenario: Takeover after the write step
- **WHEN** the write step was recorded under one claim and a later claim runs the finish step
- **THEN** the finish step checks and commits the prefix of the attempt that the backups row records

### Requirement: Takeover uses a new prefix

When a backup is reclaimed after lease expiry, the server SHALL run the next attempt under a new attempt prefix.

#### Scenario: Takeover writes new prefix
- **WHEN** another worker reclaims the backup after lease expiry
- **THEN** the new attempt writes only under its own prefix and the old prefix stays until cleanup

### Requirement: Orphan prefix cleanup

When a backup finishes, the server SHALL delete each non-live attempt prefix of that backup under the live claim.

#### Scenario: Orphan prefix deleted
- **WHEN** a backup finished on a new attempt while an older attempt prefix remains
- **THEN** the non-live prefix is deleted and the finished backup stays complete

#### Scenario: Recreated stale prefix removed again
- **WHEN** a stale writer recreates its prefix after cleanup deleted it
- **THEN** the next list removes it and the live prefix stays complete

### Requirement: Live pointer names the attempt

When the finish step runs, the server SHALL write the fixed object `backups/<backup id>/live.json`, which holds only the attempt of the backups row in one fixed byte form, from the live-claim holder, after Check passed on the prefix of that attempt, after a new check of the claim, and before the fenced finish commit.

#### Scenario: Pointer written before the finish commit
- **WHEN** Check passed on the prefix of the row attempt and the claim is live
- **THEN** the holder writes the pointer with the row attempt, and then commits finish

#### Scenario: Stale holder writes no pointer
- **WHEN** the claim moved before the pointer put
- **THEN** the holder writes no pointer and the pointer of the survivor is unchanged

### Requirement: Pointer writes give one result on every store

When the server writes the pointer, it SHALL read the pointer before the put and put it only when it is absent, SHALL read it again after `ErrExists` instead of treating `ErrExists` as the result, SHALL NOT replace a pointer that names another attempt, and SHALL give the same result on a store that refuses a second put and on a store that replaces the object.

#### Scenario: Second run of the finish step
- **WHEN** the finish step runs a second time for one backup id and the pointer already names the row attempt
- **THEN** the holder writes no pointer and goes on to the commit, on a store with conditional writes and on a store without them

#### Scenario: Two puts on a store with conditional writes
- **WHEN** two holders read no pointer, both put it, and the store refuses the second put with `ErrExists`
- **THEN** the holder that got `ErrExists` reads the pointer, finds the row attempt, and goes on to the commit

#### Scenario: Two puts on a store without conditional writes
- **WHEN** two holders read no pointer, both put it, and the store replaces the object
- **THEN** the pointer holds the row attempt in the same bytes, and each holder goes on to the commit

#### Scenario: Pointer names another attempt
- **WHEN** the pointer already names an attempt that differs from the backups row
- **THEN** the holder does not replace it on any store, the finish step fails with `backup.corrupt`, the backup does not read succeeded, and cleanup deletes nothing

### Requirement: Reader fails closed on the pointer

When a reader reads a backup, the server SHALL read the pointer with a size limit, build the attempt prefix with the attempt allowlist, require that the manifest in that prefix names the same attempt and backup id, and run Check on that prefix. A reader that has the backups row SHALL also require that the pointer attempt equals the row attempt. On any failure the server SHALL report `backup.corrupt` and SHALL NOT list the parent prefix.

#### Scenario: Missing pointer
- **WHEN** the target holds the attempt prefix and the manifest but no pointer
- **THEN** Check reports `backup.corrupt` and the reader does not list the parent prefix

#### Scenario: Pointer and manifest differ
- **WHEN** the pointer names an attempt whose manifest names another attempt or backup id
- **THEN** Check reports `backup.corrupt`

#### Scenario: Pointer and row differ
- **WHEN** the check route reads a pointer that names another attempt than the backups row
- **THEN** Check reports `backup.corrupt`

### Requirement: Cleanup keeps the pointed prefix

When cleanup runs, the server SHALL run it only after the pointer reads the attempt of the backups row, SHALL keep the prefix of that attempt, and SHALL NOT delete the prefix that the pointer names or the pointer itself.

#### Scenario: Cleanup before the pointer
- **WHEN** the pointer is not yet written
- **THEN** cleanup deletes nothing

### Requirement: Restore reads the live prefix

When a restore reads a backup, the server SHALL read only the attempt prefix named by the pointer and confirmed by the finished manifest, and SHALL run Check on that prefix before it changes anything.

#### Scenario: Restore with two prefixes
- **WHEN** a backup target holds two attempt prefixes
- **THEN** the restore reads only the attempt named by the pointer, after `Check` passes on that prefix

#### Scenario: Old unscoped layout refused
- **WHEN** the target holds only the old unscoped layout with no attempt prefix
- **THEN** the restore fails without reading files outside an attempt prefix
