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

### Requirement: Parked surface

The routes, the authorization action, the source of the second key, the docs page and the e2e step SHALL NOT be built by this change until the owner decides the questions of the proposal.

#### Scenario: Engine without a route
- **WHEN** This change lands without the parked tasks.
- **THEN** No route, no action and no setting for the rotation exists, and the engine is reached by tests only.
