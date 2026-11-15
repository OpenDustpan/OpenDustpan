## Context

Discovery runs on a runner through the runner protocol and the launcher. It must refuse unsafe input, report a preview, and record audit entries without writing tool state.

## Decisions

1. **Discovery as read-only job operation.** Discovery is a job operation named terragrunt-discover that reads Terragrunt units and writes one discovery record with unit references. It writes no state version and no history entry. A repeat claim with the same idempotency key returns the same discovery record.
2. **Launcher enforces path containment.** The launcher resolves the discovery path with symlink resolution and containment after cleaning, and it denies reads outside the working directory. The server repeats the count check and the audit write. Path enforcement lives in the launcher because the server never sees the runner working directory.
3. **Fixed discovery limits with visible effective value.** The discovery limit is a code constant of 200 units per discovery job and 300 seconds per discovery run. The effective limit shows in every preview. No operator setting changes it in this part.

## Structure

Unexported type discoveryRecord built only by newDiscoveryRecord, which checks namespace ID, unit references, count limit, and reason string before it returns a value.

## Risks

- Discovery scans a directory that a principal names, so path escape can read outside the working directory.
- A large Terragrunt tree can exceed memory or time unless count and time limits apply.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| launcher sandbox | Launcher resolves symlinks and `..` segments after cleaning and denies a path outside the working directory before any read. | Run discovery on a path with `..` and a symlinked include that points outside the working directory and check the launcher refuses it. |
| service | Service checks runner token scope for the namespace and rejects claims outside the pool namespace. | Claim a discovery job with a token from another namespace and check the claim fails. |
| handler | Handler checks the unit count limit of 200 units and the 300 second timeout before it records the preview. | Send a discovery request with 5000 units and check the handler refuses it with no discovery record. |
