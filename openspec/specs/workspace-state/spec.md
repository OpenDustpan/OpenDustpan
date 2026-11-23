# workspace-state Specification

## Purpose
This capability states how the server records and reads the client-encryption bit of a state version. The server writes the bit when it finalizes a state version and refuses a finalize without a known bit. A read of the latest version answers the bit from the row with no blob read, and streams the blob only for a version stored before the server kept the bit. The server refuses an uploaded state document that holds both encrypted_data and version.

## Requirements

### Requirement: store-encrypted-bit-at-finalize

When an OpenTofu state version finalizes, including by forced write or restore, the server SHALL store the client-encrypted bit from the upload stream in the state version row.

#### Scenario: finalize stores bit
- **WHEN** an OpenTofu state upload finalizes
- **THEN** the row holds the same bit as the uploaded document

#### Scenario: forced write and restore
- **WHEN** a forced write or a restore creates a state version
- **THEN** the new row holds the bit from its stream

### Requirement: latest-avoids-blob-read

When a caller reads the latest state version whose stored bit is present, the server SHALL answer the bit without reading the state blob.

#### Scenario: row read only
- **WHEN** the latest row holds a present bit
- **THEN** the answer uses the stored bit and performs no blob read

### Requirement: legacy-null-bit-fallback

When the latest state version row holds a null bit, the server SHALL determine the bit by streaming the state blob.

#### Scenario: legacy row
- **WHEN** the latest row holds a null bit
- **THEN** the bit is correct and the row still holds null after the read

### Requirement: refuse-both-keys

If an uploaded state document holds both encrypted_data and version, then the server SHALL refuse the document as invalid state.

#### Scenario: both keys refused
- **WHEN** an uploaded document holds both keys
- **THEN** the upload fails and no version is stored

### Requirement: writeStateVersion atomic commit

When the server commits a workspace state version, the server SHALL commit the writeStateVersion rows and the current pointer in one fenced transaction.

#### Scenario: Failed writeStateVersion commit leaves no current state
- **WHEN** the database stops in the middle of a writeStateVersion commit
- **THEN** no new state version is current and the next plan shows the prior state

#### Scenario: Two writeStateVersion writers race
- **WHEN** two writeStateVersion requests commit at the same time with different fences
- **THEN** one writeStateVersion write wins and the other is rejected with a stale-fence error

### Requirement: lockWorkspace atomic acquire

When the server acquires a workspace lock, the server SHALL commit the lockWorkspace lock row in one fenced transaction.

#### Scenario: Failed lockWorkspace acquire leaves no held lock
- **WHEN** the database stops in the middle of a lockWorkspace acquire
- **THEN** the workspace has no held lock and the next apply acquires it

#### Scenario: Two lockWorkspace callers race
- **WHEN** two lockWorkspace requests arrive at the same time
- **THEN** one lockWorkspace acquire wins and the other is rejected

### Requirement: unlockWorkspace atomic release

When the server releases a workspace lock, the server SHALL commit the unlockWorkspace release in one fenced transaction.

#### Scenario: Failed unlockWorkspace release keeps a releasable lock
- **WHEN** the database stops in the middle of an unlockWorkspace release
- **THEN** the lock stays held and a retry with the same lock identity releases it

### Requirement: lockWorkspace committed lock survives

While a workspace lock from lockWorkspace is committed, the server SHALL keep the lock held with holder identity until release or force-unlock. The lock has no expiry.

#### Scenario: Committed lockWorkspace lock stays held after instance loss
- **WHEN** the holder instance is lost after the lockWorkspace commit
- **THEN** the lock stays held on every instance and a force-unlock releases it

### Requirement: writeStateVersion idempotent retry

When the server receives a repeated writeStateVersion request with the same upload token after a lost reply, the server SHALL return the committed result without creating a duplicate state version. The retry is answered by the workspace service. A finalize of a version that is not pending stays refused by the store with `ErrUploadDenied`.

#### Scenario: writeStateVersion retry after lost reply makes no duplicate
- **WHEN** the client retries a writeStateVersion request with the same upload token after a lost reply
- **THEN** the server returns the committed result without a second state version
