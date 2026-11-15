## Context

Audit entries keep a stable hash chain. Agent identity travels in the agent block of ADR 126 decision 10 and ADR 128 decision 17, not in a struct field. The job service writes the block in the same transaction as the job change.

## Decisions

1. **Agent in block, no struct field.** Keep the audit.Entry struct unchanged. The job service supplies the agent ref to the block writer, stops writing the agent change key on new entries, and readers accept both shapes with the block winning.
2. **Same-transaction write, fail closed.** When the block write fails the job change fails and writes no entry. Entries with no block keep the unchanged hash encoding. Entries with a block cover the block bytes in verification.

## Structure

No new field on audit.Entry, so the hash of entries with no block stays unchanged; only the job service constructor maps the supplied agent ref to the block writer and fails closed on a malformed ref.

## Risks

- A reader that looks for a top-level Agent field finds none and must read the agent block.
- Old entries carry the legacy change key until retention expiry.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service passes only the supplied agent ref and digest to the block writer; entry validation refuses a malformed ref before any write. | Start a job with a malformed agent ref and show the start is refused and no entry is written. |
| port adapter | Store writes block and entry in one transaction and rolls back the job change when the block write fails. | Force the block write to fail and show the start fails and no orphan entry remains. |
| HTTP handler | Read handler returns the block unchanged and chain verification covers the block bytes. | Tamper one byte of the block and show the chain check refuses the chain. |
