## ADDED Requirements

### Requirement: StartRotation with staged key

When the operator starts a rotation, the server SHALL store a rotation in state started with the new wrapper ID derived from the staged key.

#### Scenario: Start with staged new key
- **WHEN** The operator stages the new 32-byte base64 key in the key file or variable and starts a rotation.
- **THEN** The server stores a rotation in state started with the derived new wrapper ID and writes one audit entry with the new wrapper ID.

#### Scenario: Reject key bytes in body
- **WHEN** The rotation request carries key bytes in its body.
- **THEN** The server rejects the request with no rewrap and no audit entry.

#### Scenario: Reject second start
- **WHEN** The operator starts a rotation while one rotation is active.
- **THEN** The server answers 409 with the NEW error code `installation.rotation-active`.

#### Scenario: Deny without grant
- **WHEN** A caller without the NEW action `installation:rotate-key` calls start, show, resume, abort, or finish.
- **THEN** The server answers 403.

### Requirement: RewrapNextBatch without downtime

While a rotation is active, the server SHALL open directly wrapped records that wrap under the old wrapper ID or the new wrapper ID.

#### Scenario: Read during rotation
- **WHEN** A tool reads a secret while a rotation is active.
- **THEN** The read returns plain text with no error.

#### Scenario: New writes use new wrap
- **WHEN** A write lands while a rotation is active.
- **THEN** The new record wraps under the new wrapper ID and the cursor skips it.

### Requirement: Rewrap scope and resume

When the server stops during a rewrap, the server SHALL resume the rotation from the stored cursor after restart.

#### Scenario: Resume rotation
- **WHEN** The server stops mid-batch and the operator resumes the rotation with both keys staged.
- **THEN** The rotation completes with every directly wrapped record under the new wrapper ID.

#### Scenario: Retry failed batch
- **WHEN** One rewrap batch fails on a poison record.
- **THEN** The next resume retries the same stored cursor with no skipped record, and the operator may abort after repeated failure.

### Requirement: Show and abort rotation

While a rotation is active, the server SHALL report its state and counts on show and abort it on abort.

#### Scenario: Show progress
- **WHEN** The operator shows the active rotation.
- **THEN** The server returns state started or resuming with counts of rewrapped and pending directly wrapped records.

#### Scenario: Abort leaked key
- **WHEN** The operator aborts an unfinished rotation.
- **THEN** The server marks the rotation aborted, keeps the old wrapper ID active, and writes one audit entry.

### Requirement: FinishRotation refuses old key

If a rotation finished, then the server SHALL refuse a key with the old wrapper ID.

#### Scenario: Start with old key
- **WHEN** The operator starts the server with the old key alone after the rotation finished.
- **THEN** The server refuses to start and names the key variable.

#### Scenario: Reject incomplete finish
- **WHEN** The operator finishes while some directly wrapped record still wraps under the old wrapper ID.
- **THEN** The server answers 409 with the NEW error code `installation.rotation-incomplete` and keeps state started.

#### Scenario: Start mid-rotation needs both keys
- **WHEN** The operator restarts mid-rotation with only one of `DUSTPAN_KEY_ENCRYPTION_KEY` and the NEW variable `DUSTPAN_KEY_ENCRYPTION_KEY_PREVIOUS` staged.
- **THEN** The server refuses to start and names the missing key variable.

### Requirement: Audit rotation start and finish

When a rotation starts or finishes, the server SHALL write one audit entry of kind change with the actor and both wrapper IDs.

#### Scenario: List rotation entries
- **WHEN** The operator lists audit entries for the rotation.
- **THEN** The list holds one entry for start and one entry for finish with actor, time, and wrapper IDs.

#### Scenario: Fail closed on audit write
- **WHEN** The audit write for start or finish fails.
- **THEN** The start or finish fails with no state change.

### Requirement: Restore rule across rotation

When the operator restores a backup made before a rotation finished, the server SHALL require the key that wraps that backup and fail closed without it.

#### Scenario: Restore old backup
- **WHEN** The operator restores a backup made before the rotation finished with the key that wraps it staged.
- **THEN** The restore opens the backup and rewraps its directly wrapped records under the active wrapper ID.

#### Scenario: Reject restore mid-rotation
- **WHEN** The operator restores while a rotation is active.
- **THEN** The server rejects the restore with the NEW error code `installation.rotation-active`.
