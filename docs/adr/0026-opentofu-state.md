# 26. OpenTofu state

Status: accepted · 2026-10-11

Changed by:

- ADR 31 changes: the need for a prefix list and a presigned MD5 check.
- ADR 44 changes decision 3: forced and unlocked writes need an action and a
  switch.
- ADR 44 extends decision 6: a force-unlock writes a fence version with a new
  lineage.
- ADR 45 changes decisions 4 and 5: the state size default and checks while
  the upload streams.
- ADR 220 changes decision 2: the state blob key gets a namespace prefix.
- ADR 368 changes decision 3: under a live apply claim, the server assigns the
  serial.

## Context

Part 3 of
[RFC 3](../rfc/0003-domain-modules-for-updates-secrets-and-state.md) covers
OpenTofu state, locks and the upload of state versions. A brief of 2026-10-11
checked the part against the `workspace` code, the OpenTofu source and the
go-tfe client. It informed this ADR.

The `workspace` package has the rules, but it has no SQL adapter, no
conformance suite and no wiring. The text of RFC 3 differs from the code in
some places. RFC 3 says that the size and MD5 check happens while the server
streams. The code writes the whole blob, then compares. RFC 3 also omits the
output limit, the default size limit and the key layout.

ADR 24 stores Pulumi checkpoints in SQL only. A checkpoint is one row that the
CLI overwrites, with no capability upload and no MD5. ADR 24 lists a shared
blob layout rule as open work. ADR 25 sets the scope of the TFE-compatible
API. ADR 4 sets the namespace fence. ADR 19 sets the stable ID.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts part 3 of RFC 3 with the corrections below.

1. **Storage.** The metadata of workspaces, state versions and locks lives in
   SQL. The content of a state version lives in the blob port. This differs
   from ADR 24, because each state version is a separate upload through a
   capability, and the server checks its size and MD5. A blob keeps large
   bytes out of the transaction that holds the lock.
2. **Blob keys.** A blob key is immutable. The key is
   `tofu/<workspace ID>/<state version ID>/<attempt>`. Each upload attempt
   writes a new key. The blob port gets this rule: a caller writes a key once.
   A `blobtest` case checks it. The key has no namespace segment, so an export
   for a whole namespace reads SQL.
3. **Checks on a new version.** A new state version needs the lock of the
   caller. It needs the same lineage as the current state. When no state
   exists, any lineage is valid. It needs a higher serial than the current
   state. A force upload skips the lineage check and the serial check. It never
   skips the lock check. MD5 is for integrity only. It is not a security
   control.
4. **Limits.** The size limit of a state is 512 MiB by default. The limit of
   the outputs document is 16 MiB.
5. **Order of the checks.** The server writes the blob first. It then checks
   the size and the MD5. When a check fails, the server deletes the blob of
   that attempt. This replaces the words "while streaming" in RFC 3.
6. **Locks.** The server mints a lock ID for each acquisition. The client
   never sends one. Unlock checks the holder. Force-unlock needs `unit:manage`
   (ADR 25) and answers an error when the workspace is not locked.
7. **Upload capability.** A capability is valid for 10 minutes and works one
   time. A retry of a finalized upload with the same capability and the same
   content succeeds. The finalize step checks the lock ID, the lineage and the
   serial again, in one SQL transaction, with the namespace fence (ADR 4).
   An unfenced blob write can leave an orphan blob. It can never publish a
   version.
8. **Orphans.** The server has no reaper for orphan blobs and for pending
   versions now. RFC 18 covers them later.
9. **Retention.** The server keeps every state version until RFC 18 decides
   otherwise. This is the rule of ADR 24.
10. **Identity.** A rename keeps the workspace ID and the blob prefix. A
    delete followed by a create gives a new ID (ADR 19).

## Consequences

- Open work:
  1. A conformance suite for `workspace.Store` and the state store, SQL
     adapters and numbered migrations. The suite includes a test that a
     fenced finalize leaves the old state current.
  2. The blob port rule of decision 2, with its `blobtest` case.
  3. Correct the package comment in `blob/blob.go`. It still says
     "checkpoints".
- A backup of the server needs the database and the blob store together. RFC 8
  covers backups.
- A failed finalize can leave an orphan blob until RFC 18 adds a cleanup.
- RFC 32 (S3) will need a prefix `List` on the blob port. This ADR does not add
  it. RFC 32 also decides how to check MD5 for a presigned upload.
- The decision of this ADR closes the open item in ADR 24 about a shared blob
  layout rule for OpenTofu.
