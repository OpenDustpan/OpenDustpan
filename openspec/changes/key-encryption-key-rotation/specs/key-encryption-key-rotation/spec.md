## ADDED Requirements

### Requirement: Start a rotation

When a rotation starts, the server SHALL store one rotation in state started with the old and the new wrapper ID, and SHALL write one audit entry of kind change in the scope `@server`.

#### Scenario: Start with two held keys
- **WHEN** The caller starts a rotation with a wrapper for the old key and a wrapper for the new key, and each opens one record of each non-empty family.
- **THEN** The store holds one rotation in state started, and the audit scope `@server` holds one entry with the actor, the time and both wrapper IDs, and no key bytes.

#### Scenario: Refuse a second active rotation
- **WHEN** A rotation is active and the caller starts another.
- **THEN** The start fails with NEW `ErrRotationActive` and the store holds one active rotation.

#### Scenario: Refuse equal or unusable keys
- **WHEN** The two wrapper IDs are equal, a wrapper is nil, or the old wrapper opens no record of a non-empty family.
- **THEN** The start fails, no row is written and no audit entry is written.

#### Scenario: Fail closed on audit write
- **WHEN** The audit write of the start fails.
- **THEN** The start fails and the store holds no new rotation.

### Requirement: Reads and writes during a rotation

While a rotation is active, the server SHALL open each directly wrapped record under the old key or the new key, and SHALL wrap each new record under the new key.

#### Scenario: Read during rotation
- **WHEN** A tool reads a secret, an item or the idempotency key while a rotation is active.
- **THEN** The read returns the plain value with no error, whether the record holds the old or the new wrapper ID.

#### Scenario: Write during rotation
- **WHEN** A write creates a data key or an item key while a rotation is active.
- **THEN** The new record holds the new wrapper ID and the rewrap skips it.

#### Scenario: Refuse a third key
- **WHEN** A record holds a wrapper ID that is neither the old nor the new ID.
- **THEN** The read fails with a decrypt error and the rewrap stops on that record with its scope.

### Requirement: Rewrap and resume

When the operation runs, the server SHALL wrap each data key, installation data key, item key and the idempotency server key again under the new key, in batches, and SHALL change no key value and no content.

#### Scenario: Rewrap all families
- **WHEN** The operation runs to its end.
- **THEN** No record of the four families holds the old wrapper ID, each record keeps its scope or stable ID, and each ciphertext of user data still opens to the same plain value.

#### Scenario: Resume after a stop
- **WHEN** The process stops after some batches and a new claim runs the operation.
- **THEN** The operation completes, and each record holds the new wrapper ID once.

#### Scenario: Replay a batch
- **WHEN** A batch runs again over records that already hold the new ID.
- **THEN** The batch changes no record.

#### Scenario: Item key shredded during rewrap
- **WHEN** A shred of an item key commits between the read and the write of its rewrap.
- **THEN** The rewrap deletes the new blob and the record stays shredded.

#### Scenario: Keep replay records
- **WHEN** The rewrap replaces the idempotency server key.
- **THEN** The existing replay records stay valid.

### Requirement: Finish and abort

When the caller finishes a rotation, the server SHALL change its state to finished only when no record holds the old wrapper ID, and SHALL write one audit entry.

#### Scenario: Finish with a full count
- **WHEN** No record holds the old wrapper ID and the caller finishes.
- **THEN** The rotation is finished and one audit entry holds the actor, the time, both wrapper IDs and the counts.

#### Scenario: Refuse an incomplete finish
- **WHEN** At least one record in any family still holds the old wrapper ID.
- **THEN** The finish fails with NEW `ErrRotationIncomplete`, the state stays started and no audit entry is written.

#### Scenario: Abort
- **WHEN** The caller aborts an active rotation.
- **THEN** The state is aborted, one audit entry is written, and records that hold the new ID keep it and still open while both keys are held.

### Requirement: Start-up with a rotation

When the server starts, the server SHALL refuse a process whose keys do not open every stored wrapper ID.

#### Scenario: One key while active
- **WHEN** A rotation is active and the process holds one key.
- **THEN** The server refuses to start with NEW `ErrRotationActive`, and the text names the state and both wrapper IDs.

#### Scenario: Old key after finish
- **WHEN** The rotation is finished and the process holds the old key alone.
- **THEN** The server refuses to start with `ErrWrapperMismatch` and names the key variable.

#### Scenario: Count all families
- **WHEN** An item key or the idempotency server key holds a wrapper ID that the process key does not hold.
- **THEN** The server refuses to start, as it does for a data key.

### Requirement: Restore across a rotation

When the operator restores a backup, the server SHALL need the key that wraps that backup, and SHALL refuse a restore while a rotation is active.

#### Scenario: Restore of an old backup
- **WHEN** The operator restores a backup made under the old key, and the process holds a key with a wrapper ID of the manifest.
- **THEN** The restore opens the backup. It does not rewrap by itself.

#### Scenario: Restore while active
- **WHEN** A rotation is active and the operator starts a restore.
- **THEN** The restore is refused with NEW `ErrRotationActive`.

### Requirement: Authorize a rotation

The server SHALL need the action `installation:rotate-key-encryption-key` on `@server` for each route of the rotation, SHALL give the action to the site administrator only, and SHALL need a fresh authentication step for start, finish and abort (owner, 2026-11-20).

#### Scenario: Site administrator with a fresh step
- **WHEN** The site administrator starts a rotation from a console session with a recent sign-in.
- **THEN** The server accepts the request and writes one audit entry.

#### Scenario: Other roles
- **WHEN** An account administrator or an operator calls any route of the rotation.
- **THEN** The server answers 403 and writes a denial entry.

#### Scenario: Access token on a change
- **WHEN** A request with an access token starts, finishes or aborts a rotation.
- **THEN** The server answers 403 with the code `mfa.fresh-step-required` and changes nothing.

### Requirement: Stage the old key

The server SHALL read the old key from `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` or from the file that `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE` names, SHALL wrap only under the new key, SHALL use the old key to read only, and SHALL clear the variable after the read (owner, 2026-11-20).

#### Scenario: Both keys staged
- **WHEN** A process starts with the new key and the old key.
- **THEN** It opens a record under either key, wraps each new record under the new key, and the environment holds no old key variable after the read.

#### Scenario: Refuse an unsafe file or an equal key
- **WHEN** The old key file is readable by group or others, the value is not 32 bytes of base64, or the old key equals the new key.
- **THEN** The process refuses to start with a text that names the variable.

#### Scenario: Start with no old key
- **WHEN** A caller starts a rotation and the process holds no old key.
- **THEN** The start fails with the code `key-rotation.old-key-missing` and writes no row.

### Requirement: Rotation API and command

The server SHALL give a REST API under `/api/dustpan/v1/key-rotations` to start, list, show, resume, finish and abort a rotation, with the action and the object declared on each route, and the server SHALL give the command group `opendustpan key-rotation` as a client of that API (owner, 2026-11-20).

#### Scenario: Watch the progress
- **WHEN** The site administrator runs `opendustpan key-rotation status`.
- **THEN** The command sends GET requests and prints the state, both wrapper IDs and the count of records for each family under each ID.

#### Scenario: Resume
- **WHEN** A rotation stopped and the site administrator runs `opendustpan key-rotation resume`.
- **THEN** The server claims the durable operation again and writes one audit entry.

#### Scenario: Commands that need a fresh step
- **WHEN** The site administrator runs `opendustpan key-rotation start`, `finish` or `abort`.
- **THEN** The command sends no request and prints the console path and the route.

#### Scenario: Body with a key
- **WHEN** A start request has a body field with key bytes.
- **THEN** The server refuses the request with 400 and holds no key from it.

#### Scenario: Real tools after a rotation
- **WHEN** A rotation finished and the process holds the new key alone.
- **THEN** The Pulumi CLI reads a stack secret and OpenTofu reads a workspace state, both as before the rotation.

### Requirement: Keep blobs in targets

The server SHALL delete an old item key blob in the live store only, and SHALL NOT delete it in a backup target (owner, 2026-11-20).

#### Scenario: Target keeps old blobs
- **WHEN** The rewrap finishes an item key and a backup target holds the old blob.
- **THEN** The target still holds the old blob, and a restore of that backup with the old key opens it.
