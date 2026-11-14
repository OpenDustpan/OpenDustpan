## Context

Provider serving is security code. Untrusted archives, checksums, signatures, and addresses enter at the API boundary. The store holds immutable versions in the active state and per-namespace keys.

## Decisions

1. **Atomic publish of an immutable version.** Publish is one atomic unit: the service checks host, safe name, checksum list, archive set membership, and OpenPGP signature against registered keys, then writes the release and its files in one transaction with a unique key on address plus version. A repeat with identical files succeeds without change. A repeat with different files fails as immutable. Parallel publishes give one winner and one immutable refusal, and a failed commit deletes its staged blobs.
2. **Serve from committed active rows only.** Serve reads only committed rows in the active state for the requesting namespace. The mirror index lists only active versions. The team protocol lists versions and download metadata from the same rows. Trust is checked at publish only, so key removal or expiry never changes a published version.

## Structure

Unexported type registryPublisher with constructor newRegistryPublisher that takes the namespace ID, the configured host, and the key set. Only the constructor builds a publish, and it checks host, safe name, checksums, and signature before it returns a value.

## Risks

- A weak key check lets a bad archive serve as trusted.
- A partial publish leaves an index entry without files.
- A mutable version lets a lock file change meaning.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Decode and bound the upload at the handler: allowlisted address fields, configured size limit, exact host match, safe-name check, traversal check on file names, well-formed OpenPGP packets. Reject bad input with 4xx. | Send a foreign host, an unsafe name, an oversized file, a zip bomb shape, a file name with ../, and a malformed signature packet; each gets a 4xx and no stored row. |
| service | Check the archive set, the SHA-256 list, and the OpenPGP signature against the registered namespace keys in the service. Refuse a bad checksum, a missing signature, or a bad signature before any write. | Upload a tampered archive, a missing signature, and a mismatched signature with only other-namespace keys registered; the service returns a refusal and writes nothing. |
| port adapter | Store adapter uses placeholders, one transaction for release plus files, unique key on address plus version, and a namespace predicate on each read. Keys and files are addressed by stable IDs. | Attempt SQL injection in the version string and a read of another namespace; the query treats the string as data and returns no rows. |
