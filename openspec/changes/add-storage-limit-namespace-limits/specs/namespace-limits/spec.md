## ADDED Requirements

### Requirement: Set installation limit

When the operator saves limits.storageBytes, the server SHALL store it in bytes for scope installation where 0 means unlimited at that layer.

#### Scenario: Operator sets installation cap
- **WHEN** Operator with installation settings write saves limits.storageBytes.
- **THEN** Server stores the value in bytes for scope installation with 0 meaning unlimited at that layer and shows it as part of the effective value.

#### Scenario: Invalid limit
- **WHEN** Saved limits.storageBytes is negative or not an integer.
- **THEN** Server refuses with the settings validation shape and stores nothing.

#### Scenario: Caller without grant refused
- **WHEN** Caller without installation settings write saves the installation limits.storageBytes.
- **THEN** Server refuses the save with the authorization shape.

### Requirement: Set namespace limit

When a namespace administrator saves limits.storageBytes, the server SHALL store it in bytes for scope namespace where the effective limit is the lower positive value of the installation and namespace values.

#### Scenario: Namespace narrows cap
- **WHEN** Namespace administrator with namespace settings write saves limits.storageBytes at or below the positive installation value.
- **THEN** Server stores the namespace value and answers effective limit as the lower positive value of installation and namespace values.

#### Scenario: Namespace cannot raise cap
- **WHEN** Namespace administrator saves limits.storageBytes above the positive installation value.
- **THEN** Server refuses with the settings validation shape.

#### Scenario: Installation unlimited with namespace cap
- **WHEN** Installation value is 0 and the namespace value is positive.
- **THEN** Server answers effective limit equal to the namespace value.

#### Scenario: Namespace unlimited with installation cap
- **WHEN** Namespace value is 0 and the installation value is positive.
- **THEN** Server answers effective limit equal to the installation value.

#### Scenario: Default unlimited
- **WHEN** Installation value is 0 and namespace value is 0.
- **THEN** Server answers effective limit 0 meaning unlimited.

#### Scenario: Caller without grant refused
- **WHEN** Caller without namespace settings write saves the namespace limits.storageBytes.
- **THEN** Server refuses the save with the authorization shape.

### Requirement: Refuse new writes above limit

When a new state write outside work in progress would move namespace use above its effective storage limit, the server SHALL refuse the write through CheckStorageLimit with used bytes and limit bytes.

#### Scenario: CheckStorageLimit refuses Pulumi write
- **WHEN** CheckStorageLimit finds a new Pulumi state write with no held lease would move use above the effective limit.
- **THEN** Pulumi Cloud-compatible API answers with the Pulumi error shape that names storage limit with used bytes and limit bytes, and no staged blob is counted as use.

#### Scenario: CheckStorageLimit refuses OpenTofu write
- **WHEN** CheckStorageLimit finds a new OpenTofu state write with no held run would move use above the effective limit.
- **THEN** TFE-compatible API answers with the JSON:API error shape that names storage limit with used bytes and limit bytes, and no staged blob is counted as use.

#### Scenario: CheckStorageLimit allows exact limit
- **WHEN** Use plus write size equals the effective limit.
- **THEN** Server allows the write.

#### Scenario: CheckStorageLimit exempts work in progress
- **WHEN** CheckStorageLimit sees the write carries the held lease or run ID of an update, run, or job phase in progress.
- **THEN** Server allows lease renewal, checkpoints, events, log chunks, and the final state write of the held lease or run.

#### Scenario: CheckStorageLimit serializes concurrent writes
- **WHEN** Two new writes race with only room for one under the locked namespace row.
- **THEN** One writer passes and the other refuses with used and limit bytes.

#### Scenario: Suspended namespace refuses writes
- **WHEN** A new state write targets a suspended or disabled namespace.
- **THEN** Server refuses the write with the namespace state shape.

### Requirement: Lower limit below use

When a limit drops below current namespace use, the server SHALL keep stored data and refuse only new growing writes.

#### Scenario: Existing data stays
- **WHEN** Operator or namespace administrator lowers a limit below current use.
- **THEN** Stored state, checkpoints, history, and artifacts stay readable and only new growing writes refuse.

#### Scenario: Later lower installation value applies
- **WHEN** Installation limit drops below the stored namespace value.
- **THEN** Server answers the lower installation value as the effective limit at once.

### Requirement: Allow deletes and reads above limit

While namespace use is above its effective storage limit, the server SHALL allow deletes, reads, locks, unlocks, and a state write that lowers use.

#### Scenario: Delete above limit
- **WHEN** Namespace use is above the effective limit.
- **THEN** Delete runs and lowers the use counter.

#### Scenario: Read above limit
- **WHEN** Namespace use is above the effective limit.
- **THEN** State and history reads answer as normal.

#### Scenario: Smaller state allowed
- **WHEN** A state write above the limit lowers total use.
- **THEN** Server allows the write and lowers use.

#### Scenario: Lock and unlock allowed
- **WHEN** Namespace use is above the effective limit.
- **THEN** Server allows workspace lock and unlock above the limit.

### Requirement: Account use

When the server counts namespace storage, the server SHALL sum state, checkpoint, history, and artifact bytes in bytes for scope namespace.

#### Scenario: Sum of four sources
- **WHEN** A write changes stored bytes.
- **THEN** Server adds state, checkpoint, history-item, and artifact bytes to the per-namespace use counter in the same fenced transaction.

#### Scenario: Recompute after restart
- **WHEN** The server restarts or a delete completes.
- **THEN** Background recompute sums stored rows and blob sizes and resets the counter.

### Requirement: Near-limit record

When namespace use reaches 80 percent of its positive effective storage limit, the server SHALL record the NEW near-limit record once per crossing.

#### Scenario: Cross 80 percent
- **WHEN** Use reaches 80 percent of the positive effective limit in bytes for scope namespace.
- **THEN** Server records the NEW near-limit record once with used bytes and limit bytes on the settings resource.

#### Scenario: Reset below mark
- **WHEN** Use drops below 80 percent of the effective limit.
- **THEN** Server arms the record again.

#### Scenario: Unlimited has no record
- **WHEN** Effective limit is 0.
- **THEN** Server records no near-limit record.

### Requirement: Audit refusals

When storage-limit refusals occur, the server SHALL write 1 audit entry per minute for scope namespace with the refusal count.

#### Scenario: One entry per minute
- **WHEN** One or more storage-limit refusals occur in one minute across replicas and restarts.
- **THEN** Server writes one audit entry for the namespace scope with the count of refused writes from the store counter.
