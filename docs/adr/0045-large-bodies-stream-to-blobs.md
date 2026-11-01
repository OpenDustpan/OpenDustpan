# 45. Large request bodies stream to blob storage

Status: accepted · 2026-10-12

Changed by:

- ADR 62 changes decision 2: the reservation of the outputs document and its
  seal.

## Context

[RFC 58](../rfc/0058-large-request-limits.md) found that large request
bodies use several copies of memory each. Pull request #241 shipped most of
its proposal without an ADR:

- The default size limits are 32 MiB for a Pulumi checkpoint
  (`--max-checkpoint-bytes`), 32 MiB for a secrets batch
  (`--max-secret-batch-bytes`) and 32 MiB for an OpenTofu state
  (`--max-state-bytes`).
- Large Pulumi bodies share one budget of bytes in flight
  (`--max-body-bytes-in-flight`, default 128 MiB). A body reserves its limit
  before the server reads it. A full budget answers 503 with `Retry-After`.
- Gzip decompression streams into the JSON decoder, and the server bounds both the wire size and
  the decompressed size.

Three gaps remain:

- An OpenTofu state upload reads the whole body into memory
  (`workspace.Service.Upload`) and is not under the budget.
- The JSON decoder buffers the whole value, so one Pulumi checkpoint can use
  about two times its limit while the budget counts it one time.
- Memory still grows with the size of the state, so the limits protect the
  server instead of expressing a policy.

The blob port already takes a stream (`blob.Store.Put(ctx, key, io.Reader)`),
and both tools write the blob before the SQL row (ADR 26, ADR 37). On
2026-10-12 the project owner approved streaming for both tools, and for the
Pulumi CLI the option that stores the whole request body as received.

## Decision

1. **The limits are policy.** The defaults above stay, with their flags.
   They replace the 256 MiB limit of ADR 24 and the batch limit of ADR 23.
   The limit applies to the decompressed body.
2. **One budget for both tools.** OpenTofu state uploads join the budget of
   large bodies. A body that streams reserves a fixed cost for its buffers,
   not its limit, so the budget caps the number of large bodies in flight.
   A body that does not stream yet reserves its limit, as today.
3. **OpenTofu uploads stream.** The upload flows through the size limit, the
   MD5 hash and the seal of ADR 41 into `blob.Store.Put`, in one pass. When
   the MD5 differs from the declared MD5, the server deletes the blob and
   refuses the upload. The version does not finalize. Anything that the
   finalize step reads from the state comes from the same pass, through a
   streaming decoder.
4. **Pulumi checkpoints store the body as received.** For `checkpoint` and
   `checkpointverbatim`, the blob holds the decompressed request body, byte
   for byte, with gzip as in ADR 37. In the same pass, a streaming decoder
   finds the byte range of the deployment value in the body and computes its
   SHA-256 and size. The SQL row keeps the offset and the length of that
   range. This changes decision 1 of ADR 37 (the blob holds the envelope) and
   keeps decision 4 (the deployment bytes are exactly as the CLI sent them).
5. **Reads take the range.** Export, delta bases and journal bases read the
   blob as a stream and copy only the deployment range. A row from before
   this ADR has no offset: its blob holds only the deployment, and a read
   takes the whole blob. No data moves.
6. **Delta results.** A delta reads the deployment range of the base as a
   stream, applies the edits in order and streams the result into a new blob.
   The new blob holds only the deployment, with offset 0, and the hash check
   of ADR 37, decision 7, applies to it.
7. **Schema.** A new migration adds two nullable columns to the checkpoint
   row: the offset and the length of the deployment range. Backups (ADR 30)
   carry them.

## Consequences

- Memory for one large body no longer grows with the state. The size limits
  stay as a policy that an operator can raise.
- The stored blob of a full save is a little larger than the deployment,
  because it keeps the envelope.
- Two blob layouts exist: an envelope with a range, and a deployment only.
  The row says which one applies, so a reader never guesses.
- The streaming decoder of decision 4 must accept the same bodies as today,
  or fail closed with 400. A test compares the range with the deployment that
  the current decoder extracts, for each body in the e2e suite.
- The budget keeps its 503 for bursts of large bodies. Its default may go
  down after a measurement.
