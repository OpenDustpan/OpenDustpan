## Context

The OpenTofu States adapter streams the whole state blob to learn the client-encrypted bit on each outputs read. The upload already computes the bit while the bytes pass through metaStream at finalize.

## Decisions

1. **Store the bit at finalize in the baseline and read the row in Latest.** Add a nullable client-encrypted column to the state version row in each dialect baseline in name order, with no numbered migration file. The declare step creates the pending row with a null bit. The finalize step finishes the metaStream before commit, learns the bit from finish, then in the same fenced transaction that writes the item-key record writes the bit into the row and exposes the version. Blob bytes are staged before that transaction; a crash before commit leaves an unreferenced pending blob and no visible version, and no row without a bit is ever exposed. Read the bit in Latest with one row read and no blob access when the bit is present; fall back to the existing streamed read only when the bit is null, and never write the fallback result back. In the domain type the finalized row uses a non-nullable bool set by one constructor that rejects an unset bit; null exists only at the SQL layer for legacy rows and pending rows, and Latest maps null to the fallback path. Forced writes and restores use the same finalize path and the same constructor, so they store the bit too.
2. **Refusal cleanup and shred precedence.** A refusal abandons the metaStream with abandonStream, deletes the pending declare row in the same transaction, and stores no version. A shred or redaction check runs before any outputs answer: a shredded or redacted latest item answers shredded or redacted even when the stored bit is present, and the outputs path still needs the blob for plaintext answers.

## Structure

Unexported finalize writer sets the row field through one constructor which rejects an unset encrypted bit; no other constructor exists so a new version cannot finalize without the bit. A mutation test removes the rejection and the conformance test fails.

## Risks

- Rows written before the change hold null and pay the old streamed-read cost until a new finalize replaces them; the next finalize of each unit removes the cost. This change adds no backfill and no write-back.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| latency | 0.05 | s | outputs read of latest version with present bit |
| blob-read | 0 | bytes | outputs read of latest version with present bit |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Finalize state version | Blob write succeeds but row commit fails | Retry finalize with a new upload; pending blob stays unreferenced | No new latest version visible |
| Finalize state version | Upload holds both encrypted_data and version | Do not retry the same document; upload a valid state | Pending declare row removed, no version stored |
