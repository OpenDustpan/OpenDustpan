## Context

Own-CI teams run the plan outside OpenDustpan. The server never sees the run, so it cannot trust the plan the same way. The design stores the uploaded bytes as a stored plan, marks the change order weaker, and refuses any apply that does not present both hashes of the approved plan.

## Decisions

1. **Upload makes a marked change order.** POST makes one change order with one unit plan from the two uploaded parts, sealed with one item key per ADR 102. The change order carries uploaded=true and shows the uploaded-plan mark. Approvals bind to the plan ID, both part hashes, and the state version.
2. **Apply checks both hashes at the write.** The state write for OpenTofu and the update start for the Pulumi CLI carry both plan part hashes. The server compares both hashes and the state version with the approved stored plan inside the write transaction and refuses a mismatch or a missing hash with the change order ID.
3. **One transaction fences races.** One fenced transaction per change order serializes a second upload and a concurrent state write. A second upload supersedes the prior unit plan, moves the revision, and voids its approvals. A state write that lands before the supersede passes against the old revision; one that lands after fails the hash check and names the current revision. Blob writes before the plan transaction reuse the atomicity of ADR 102 decision 5; orphan blobs from a crash before commit fall to existing orphan cleanup and the client retries the upload.

## Structure

Unexported type uploadedPlan with constructor checkUploadedPlan that checks part kinds, sizes, and hashes; only the constructor makes a value.

## Risks

- A team mistakes an uploaded plan for a runner plan and trusts it the same way.
- An old approved hash is replayed after a new upload.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| API boundary | Route checks unit:run-plan on the unit and seals both parts before the store transaction. | Send an upload with no grant and with one part larger than 256 MiB; each fails without a stored plan. |
| service | Service checks the SHA-256 of each uploaded part against the claimed hash, stores both hashes on the unit plan, and binds approvals to both hashes. | Upload bytes whose hash differs from the claimed hash; the service refuses the upload. |
| store write | State write and update start compare both presented hashes with the approved stored hashes in the write transaction, including absent hash and replayed old hash. | Write state with an old hash after approval of a new upload, and with no hash; each write fails with the change order named. |
