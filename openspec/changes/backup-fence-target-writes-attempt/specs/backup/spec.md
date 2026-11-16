## ADDED Requirements

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
