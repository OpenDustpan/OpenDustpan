## Context

The upload already computes the bit in metaStream. Latest streams the blob again to learn it.

## Decisions

1. **Store the bit at finalize and read the row in Latest.** Add a nullable client-encrypted column to the state version row in each dialect baseline. Finalize finishes the metaStream before commit, then writes the bit in the same fenced transaction that exposes the version. Latest reads the row and streams the blob only when the bit is null. Forced writes and restores use the same path.
2. **Refusal cleanup and shred precedence.** A refusal calls abandonStream, deletes the pending row and stores no version. A shred or redaction check runs before any outputs answer.

## Structure

The finalized domain value holds a non-nullable bool made by one constructor that rejects an unset bit. Null exists only in SQL for legacy and pending rows.

## Risks

- Legacy rows keep the streamed cost until their unit finalizes a new version.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| blob-read | 0 | bytes | Latest with a present bit |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Finalize | Blob write succeeds and the row commit fails | Upload again | No new latest version is visible |
