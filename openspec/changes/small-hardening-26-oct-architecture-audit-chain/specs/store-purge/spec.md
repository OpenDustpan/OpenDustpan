## ADDED Requirements

### Requirement: purge-frees-skipped-blobs

When final purge runs for a soft-deleted stack, the server SHALL free blobs skipped by the stack sweep.

#### Scenario: final purge after soft delete
- **WHEN** final purge runs for a soft-deleted stack
- **THEN** blobs skipped by the stack sweep are freed

#### Scenario: still referenced blob
- **WHEN** a skipped blob is still named by another live stack
- **THEN** purge keeps the blob and frees only unreferenced blobs

#### Scenario: interrupted purge
- **WHEN** final purge stops before it frees each skipped blob
- **THEN** a restarted purge frees the remaining skipped blobs
