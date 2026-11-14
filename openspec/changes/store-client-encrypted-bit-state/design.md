## Context

The OpenTofu States adapter streams the whole state blob to learn the client-encrypted bit on each outputs read. The upload already computes the bit while the bytes pass through metaStream at finalize.

## Decisions

1. **Store the bit at finalize in the baseline and read the row in Latest.** Add a nullable client-encrypted column to the state version row in each dialect baseline in name order, with no numbered migration file. Write it once at finalize from the streamed meta in the same fenced transaction that exposes the version: blob bytes first, then the row commit that makes the version visible. A crash between the two leaves an unreferenced pending blob that no Latest reads; a later finalize computes its own bit from its own stream. Read the bit in Latest with one row read and no blob access when the bit is present; fall back to the existing streamed read only when the bit is null, and never write the fallback result back.

## Structure

Unexported finalize writer sets the row field through one constructor which rejects an unset encrypted bit; no other constructor exists so a new version cannot finalize without the bit. A mutation test removes the rejection and the conformance test fails.

## Risks

- Rows written before the change hold null and pay the old streamed-read cost until a new finalize replaces them.
