## ADDED Requirements

### Requirement: Start a rotation

When a rotation starts, the server SHALL store one rotation in state started with the old and the new wrapper ID, and SHALL write one audit entry of kind change in the scope `@server` in the same transaction.

#### Scenario: Start with two held keys
- **WHEN** The caller starts a rotation with a wrapper for the old key and a wrapper for the new key, and each opens one record of each non-empty family that holds its ID.
- **THEN** The store holds one rotation in state started, and the audit scope `@server` holds one entry with the actor, the time and both wrapper IDs, and no key bytes.

#### Scenario: Refuse a second active rotation
- **WHEN** A rotation is active and the caller starts another.
- **THEN** The start fails with NEW `ErrRotationActive` and the store holds one active rotation.

#### Scenario: Refuse equal or unusable keys
- **WHEN** The two wrapper IDs are equal, a wrapper is nil, or the old wrapper opens no record of a non-empty family that holds its ID.
- **THEN** The start fails, no row is written and no audit entry is written.

#### Scenario: Refuse a third wrapper ID
- **WHEN** A record of one of the four families holds a wrapper ID that is neither the old nor the new ID.
- **THEN** The start fails with the family and the count, and writes no row.

#### Scenario: Refuse while a backup runs
- **WHEN** A backup runs and the caller starts a rotation.
- **THEN** The start fails with NEW `ErrBackupRunning` (409 `key-rotation.backup-running`) and writes no row.

#### Scenario: Fail closed on audit write
- **WHEN** The audit write of the start fails.
- **THEN** The start fails and the store holds no new rotation.

### Requirement: Instance wrapper IDs

Each process SHALL write the wrapper IDs that it holds in the same transaction as its instance row. Start and finish SHALL refuse while a live instance row lacks an ID of the rotation.

#### Scenario: Register the IDs of a plain wrapper
- **WHEN** A process with a plain wrapper writes its instance row.
- **THEN** The row has one wrapper ID.

#### Scenario: Register the IDs of a rotating wrapper
- **WHEN** A process with a `Rotating` wrapper writes its instance row.
- **THEN** The row has both wrapper IDs.

#### Scenario: Refuse start while an instance lacks a key
- **WHEN** An instance row holds one ID only and the caller starts a rotation.
- **THEN** The start fails with `ErrInstanceLacksKey` (409 `key-rotation.instance-lacks-key`), the answer names the instance ID, and no row is written.

#### Scenario: A refused process leaves no instance row
- **WHEN** The start-up checks of the table refuse a process.
- **THEN** The process wrote no instance row, because `registerInstance` runs after the checks.

#### Scenario: Refuse finish while an instance lacks a key
- **WHEN** The rewrap is complete and an instance row lacks an ID.
- **THEN** The finish fails with `ErrInstanceLacksKey` and the rotation stays started.

#### Scenario: A stale row is removed by an administrator
- **WHEN** The row of a process that is gone blocks the start and the administrator deletes it with `DELETE /api/server/instances/{id}`.
- **THEN** The next start does not count that row.

### Requirement: Reads and writes during a rotation

While a rotation is active, the server SHALL open each directly wrapped record under the key that its stored wrapper ID names, with no trial of another key. A process that holds both keys SHALL wrap under the old key until a rotation is stored, and under the new key after.

#### Scenario: Process with both keys before any rotation wraps under the old key
- **WHEN** A process holds a `Rotating` wrapper, no rotation row exists, and a write creates a data key.
- **THEN** The new record holds the old wrapper ID, and a process that holds the old key alone opens it.

#### Scenario: Write after the start wraps under the new key
- **WHEN** A write creates a data key or an item key while a rotation is started.
- **THEN** The new record holds the new wrapper ID and the rewrap skips it.

#### Scenario: Write fence refuses a stale generation
- **WHEN** A write wrapped a record under generation 0, and a start commits (generation 1) before the write transaction commits.
- **THEN** The write fails once with NEW `ErrRotationFence`, writes nothing, and the service wraps again under the new key and writes again.

#### Scenario: Fence with no rotation row
- **WHEN** No rotation row exists, or the latest row is finished, and a stamped write meets a start and a finish between its wrap and its commit.
- **THEN** The singleton row exists in both cases, the write sees generation 2 and fails with `ErrRotationFence`, and no record holds the old ID after the finish count.

#### Scenario: PostgreSQL writer holds its fence read across start and finish
- **WHEN** A PostgreSQL writer holds its `FOR SHARE` read of the singleton row, a start runs, and the writer then commits.
- **THEN** The start waits for the writer, and a writer that read the row before a start and a finish and commits after both fails with `ErrRotationFence`.

#### Scenario: Concurrent instance write or backup begin against a start
- **WHEN** An instance row write or a backup begin runs at the same time as a start.
- **THEN** Either the start reads that row or that backup and refuses, or the write or the begin waits for the start and fails its own check. No start commits next to a row or a backup that it did not read.

#### Scenario: Fence applies only while a rotation is started
- **WHEN** A rotation A to B is finished, a backup that holds only A IDs is restored, and a plain A process starts (row 9 of the table).
- **THEN** The process writes new records under A with no refusal, because no rotation is started and a plain wrapper gives no stamp.

#### Scenario: Wrap fails closed on a state read error
- **WHEN** The read of the rotation state fails during `Wrap`.
- **THEN** `Wrap` returns the error, wraps nothing, and does not fall back to the old key.

#### Scenario: Wrap fails closed on an unheld new key
- **WHEN** The latest row names a new wrapper ID that the process does not hold.
- **THEN** `Wrap` returns `ErrWrapperMismatch` and wraps nothing.

#### Scenario: Page cursors work while both keys are staged
- **WHEN** A list route seals a page cursor and opens it while the process holds a `Rotating` wrapper.
- **THEN** The cursor carries the wrapper ID, opens by that ID, and the list continues. A cursor with an ID that the wrapper does not hold, with a length byte of 0, or sealed for another AAD fails with the invalid cursor answer.

#### Scenario: Cursor of the old key after finish
- **WHEN** A cursor was sealed under the old key and the rotation is finished.
- **THEN** The open fails with the invalid cursor answer and the caller lists again.

#### Scenario: Read during rotation
- **WHEN** A tool reads a secret, an item or the idempotency key while a rotation is active.
- **THEN** The read returns the plain value with no error, whether the record holds the old or the new wrapper ID.

#### Scenario: Unwrap by the stored ID
- **WHEN** A record holds the old wrapper ID and the process holds both keys.
- **THEN** The open uses the old key only, and the open of a record that holds a third ID fails with `ErrDecrypt` with no attempt under a held key.

#### Scenario: Refuse a third key
- **WHEN** A record holds a wrapper ID that is neither the old nor the new ID.
- **THEN** The read fails with a decrypt error and the rewrap stops on that record with its scope.

### Requirement: Rewrap and resume

When the operation runs, the server SHALL wrap each data key, installation data key, item key and the idempotency server key again under the new key, in batches, and SHALL change no key value and no content. The operation SHALL only move forward: start, resume, finish.

#### Scenario: Rewrap all families
- **WHEN** The operation runs to its end.
- **THEN** No record of the four families holds the old wrapper ID, each record keeps its scope or stable ID, and each ciphertext of user data still opens to the same plain value.

#### Scenario: Resume after a stop
- **WHEN** The process stops after some batches and a new claim runs the operation.
- **THEN** The operation completes, and each record holds the new wrapper ID once.

#### Scenario: Resume claimed by another instance
- **WHEN** Another instance holds a live claim of the operation and a caller resumes.
- **THEN** The answer is 409 `key-rotation.claimed` and no batch runs.

#### Scenario: Resume finds a late old-ID record
- **WHEN** A pass ends and a record holds the old ID.
- **THEN** The next pass moves it, and finish refuses until no record holds the old ID.

#### Scenario: Replay a batch
- **WHEN** A batch runs again over records that already hold the new ID.
- **THEN** The batch changes no record.

#### Scenario: Record a failed batch record
- **WHEN** A record opens under no held key.
- **THEN** The step fails, one audit entry of outcome `failure` holds the family and the scope or stable ID, the log holds the same fields and no key bytes, and a resume that fails again writes a new entry.

#### Scenario: Item key shredded during rewrap
- **WHEN** A shred of an item key commits between the read and the write of its rewrap.
- **THEN** The rewrap deletes the new blob, writes no pending delete and the record stays shredded.

#### Scenario: Keep replay records
- **WHEN** The rewrap replaces the idempotency server key.
- **THEN** The replay records stay and replay as before.

### Requirement: Durable pending blob deletes

The rewrap of an item key SHALL write one pending delete for the old blob in the same transaction as the record, and SHALL delete the old blob only after that commit.

#### Scenario: Crash between the commit and the blob delete
- **WHEN** The process stops after the commit of the record and before the delete of the old blob.
- **THEN** One pending delete remains, the next pass deletes the blob and the row, and a delete of a blob that is gone counts as done.

#### Scenario: Finish refuses with a pending delete
- **WHEN** The count of old IDs is zero and one pending delete remains.
- **THEN** The finish fails with `ErrRotationIncomplete` (409 `key-rotation.incomplete`) and the rotation stays started.

### Requirement: Finish

The server SHALL finish a rotation only when no record of the four families holds the old ID, no pending delete remains and each instance row holds both IDs, all read in the transaction that sets the state.

#### Scenario: Finish with a full count
- **WHEN** The caller finishes after the last batch.
- **THEN** The state is finished, one audit entry holds the counts, and the old wrapper ID is refused at start.

#### Scenario: Finish with one old record left in a family
- **WHEN** One record of a family still holds the old ID.
- **THEN** The finish fails with `ErrRotationIncomplete` and names the counts of each family.

#### Scenario: Finish races with an old-ID write
- **WHEN** A write of an old-ID record runs while the finish transaction runs.
- **THEN** The write fails with `ErrRotationFence`, or the finish counts the record and fails. No record holds the old ID after a committed finish.

#### Scenario: Finish a finished rotation
- **WHEN** The caller finishes or resumes a rotation that is finished.
- **THEN** The answer is 409 `key-rotation.not-active` and nothing changes.

#### Scenario: No abort
- **WHEN** A rotation is started.
- **THEN** The server has no route, command, code or audit action that cancels it. To go back to the old key, the operator finishes, then starts a rotation in the other direction.

### Requirement: Start-up with a rotation

The server SHALL decide at start-up from the latest rotation row, the wrapper of the process and the stored wrapper IDs of the four families, by the table of the scenario "Each row of the table".

#### Scenario: Each row of the table
- **WHEN** The server starts with the wrapper and the store of one row. S is the set of stored IDs, P the ID of a plain wrapper, O and N the IDs of a `Rotating` wrapper, R the latest row.
- **THEN** The result is the result of that row of the table below, and the test `TestStartUpMatrix` has one case for each of the 13 rows and for each of the four families.

| # | Row R | Wrapper | Stored IDs S | Result |
| --- | --- | --- | --- | --- |
| 1 | none | plain P | empty or {P} | start |
| 2 | none | plain P | an ID other than P | refuse, `ErrWrapperMismatch` |
| 3 | none | Rotating (O, N) | inside {O, N} | start |
| 4 | none | Rotating (O, N) | an ID outside {O, N} | refuse, `ErrWrapperMismatch` |
| 5 | started | plain | any | refuse, `ErrRotationActive` |
| 6 | started | Rotating with {O, N} not equal to {R.old, R.new} | any | refuse, `ErrRotationActive` |
| 7 | started | Rotating (R.old, R.new) | inside {O, N} | start |
| 8 | started | Rotating (R.old, R.new) | an ID outside {O, N} | refuse, `ErrWrapperMismatch` |
| 9 | finished | plain P | {P} | start |
| 10 | finished | plain P | an ID other than P | refuse, `ErrWrapperMismatch` |
| 11 | finished | Rotating with O equal to R.old | any | refuse, `ErrOldKeyHeld` |
| 12 | finished | Rotating with O not equal to R.old | inside {O, N} | start |
| 13 | finished | Rotating with O not equal to R.old | an ID outside {O, N} | refuse, `ErrWrapperMismatch` |

#### Scenario: Old key held after finish
- **WHEN** The rotation is finished and the process still holds the old key (row 11).
- **THEN** The start fails with `ErrOldKeyHeld`, the text names `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`, says to remove the one that is set, and names the command `opendustpan serve` with the new key alone. The server writes no warning and starts no listener.

#### Scenario: Plain wrapper refusal keeps the word rewrap
- **WHEN** A plain wrapper meets a store with a wrapper ID other than its own and no rotation exists (row 2).
- **THEN** The text keeps the word "rewrap" and names `opendustpan key-rotation`.

#### Scenario: No key and no wrapped key
- **WHEN** The process has no key and the store holds no wrapped key.
- **THEN** The server starts as on main. A process with no key and a store with a wrapped key fails with `ErrKeyNotConfigured`.

#### Scenario: Idempotency key with an unheld ID
- **WHEN** The idempotency key row holds an ID that the wrapper does not hold.
- **THEN** The start fails at the table and never reaches the reset path. A held ID that fails to open (damage) returns an error and does not reset the key while a rotation is started. After the rotation is finished the reset path works as on main.

### Requirement: Restore across a rotation

A restore SHALL need a key for every wrapper ID of the manifest, and SHALL be refused while a rotation is started.

#### Scenario: Manifest with two IDs and a plain wrapper
- **WHEN** The manifest holds two wrapper IDs and the wrapper holds one of them.
- **THEN** The restore fails with `ErrWrapperMismatch` for the ID that the wrapper does not hold, and the check does not accept one ID of the two.

#### Scenario: Manifest with two IDs and a rotating wrapper
- **WHEN** The manifest holds the old and the new ID and the wrapper holds both.
- **THEN** The key check passes.

#### Scenario: Restore while a rotation is started
- **WHEN** A rotation is started and a restore starts.
- **THEN** The restore fails in `admitStart` with 409 `restore.key-rotation-active` and changes nothing.

### Requirement: Backup during a rotation

The server SHALL refuse a backup while a rotation is started, and SHALL refuse a rotation start while a backup runs. This rule is reviewer-prescribed.

#### Scenario: Backup after a start
- **WHEN** A rotation is started and a backup starts.
- **THEN** The backup fails with `ErrRotationActive` (409 `backup.key-rotation-active`) and writes no backup row.

#### Scenario: Scheduled backup refused
- **WHEN** A scheduled backup meets a started rotation.
- **THEN** The refusal is logged and one audit entry of outcome `failure` is written.

#### Scenario: Backup that ends after a start
- **WHEN** A rotation starts after a backup started and before it writes its manifest.
- **THEN** The generation of the singleton row is not the generation that the backup stored at its begin, the backup fails and writes no manifest. The check compares the generation, not a wall clock.

### Requirement: Authorize a rotation

The server SHALL authorize start, resume, finish and the reads of a rotation with the action `installation:rotate-key-encryption-key` on `ServerObject()`, at the route and again at the service. Only the site administrator SHALL hold the action, and start and finish SHALL need a fresh step.

#### Scenario: Site administrator with a fresh step
- **WHEN** The site administrator starts or finishes with a console session that has a fresh step.
- **THEN** The call is allowed.

#### Scenario: Other roles at the route
- **WHEN** An account administrator, an operator or a namespace administrator calls any of the five routes.
- **THEN** The answer is 403 and the denial entry is written.

#### Scenario: Caller without the action at the service
- **WHEN** A caller without the action calls `Start`, `Resume`, `Finish`, `Show` or `List` of the service, as if the route check were missing.
- **THEN** The call returns `ErrRotationDenied`, writes one audit entry of outcome `denied` with the action and the object, and changes nothing. A check that cannot run returns `ErrAccessUnchecked`. A test that removes the service check fails.

#### Scenario: Missing fresh check at the service
- **WHEN** `Start` or `Finish` gets a nil fresh check or one that returns an error.
- **THEN** The call fails with the fresh-step error and changes nothing.

#### Scenario: Access token at the route
- **WHEN** A caller sends an access token to start or finish.
- **THEN** The answer is `mfa.fresh-step-required`.

### Requirement: Stage the old key

The process SHALL read the old key from `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` or `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`, never from a flag.

#### Scenario: Value and file together
- **WHEN** Both variables are set.
- **THEN** The start fails with a text that names both variables.

#### Scenario: Variables are sensitive
- **WHEN** The process has started.
- **THEN** Both variables are in `SensitiveEnvs`, `clearSensitiveEnv` clears them, and a read of the process environment by name finds neither.

#### Scenario: Zero the old key bytes
- **WHEN** The old wrapper is built.
- **THEN** The decoded bytes and the raw text are zeroed after use.

#### Scenario: Strict file open
- **WHEN** The old key file is not a regular file, has a mode bit for group or others, or is a link that leads outside its directory (a link to a path outside the directory, or an absolute link).
- **THEN** The start fails and names `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`. The file opens through `os.OpenRoot` of its directory and `root.Open` of its name, and the mode and the file type are read from the open descriptor.

#### Scenario: Kubernetes Secret volume
- **WHEN** The old key file is a relative link such as `key` to `..data/key` and `..data` is a link to a directory, all inside the one directory, and the file has mode 0400.
- **THEN** The file opens and the key loads.

#### Scenario: Bad value
- **WHEN** The value is not base64, or does not decode to 32 bytes, or its wrapper ID equals the ID of the new key.
- **THEN** The start fails and the text holds no key bytes.

### Requirement: Rotation API and command

The server SHALL give five routes under `/api/dustpan/v1/key-rotations`, and the command `opendustpan key-rotation` SHALL be a client of them. The command SHALL read no key variable.

#### Scenario: Routes
- **WHEN** A caller with the action calls `POST /key-rotations`, `GET /key-rotations`, `GET /key-rotations/{id}`, `POST /key-rotations/{id}/resume` and `POST /key-rotations/{id}/finish`.
- **THEN** Start answers 202 with an `Idempotency-Key`, the reads answer state, wrapper IDs, counts of each family, pending deletes and the operation state, and no route answers with key bytes. There is no abort route.

#### Scenario: Strict decode
- **WHEN** The start body holds a field, for example `key`.
- **THEN** The answer is 400 and the field is not stored or logged.

#### Scenario: Status and resume
- **WHEN** The user runs `opendustpan key-rotation status` or `resume` with a token.
- **THEN** The command sends the matching requests and prints the result.

#### Scenario: Start and finish print the path
- **WHEN** The user runs `opendustpan key-rotation start` or `finish`.
- **THEN** The command sends no request and prints the documented path (password sign-in as a local user, read of the CSRF value, the call with the cookie, the CSRF header and `Origin`) and the page `docs/guides/key-rotation.md`.

#### Scenario: Documented API path
- **WHEN** A person follows the page with a local-user password sign-in, on an installation whose MFA policy asks site administrators for no second factor. Whether the path also works under a policy that asks for one is an open owner question (see the proposal) and this change does not claim it.
- **THEN** The session has a CSRF value, and start and finish with the cookie, the CSRF header and `Origin` pass the fresh step.

### Requirement: Keep blobs in targets

The rewrap SHALL delete the old item key blob in the live store only, and SHALL hold no backup target.

#### Scenario: Targets keep old blobs
- **WHEN** The rewrap of an item key ends and a backup target holds the old blob.
- **THEN** The live store does not hold the old blob and the target does.

### Requirement: Document the retained old wraps

The user documentation SHALL say that old wraps stay in database history and in backups.

#### Scenario: First section of the page
- **WHEN** A person reads `docs/guides/key-rotation.md`.
- **THEN** The first section says that a rotation does not protect against a person who holds the old key and a copy of the database or a backup from before the finish, and names the vacuum and the delete of old backups as steps of the operator.
