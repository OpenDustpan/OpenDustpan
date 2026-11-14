## Context

Provider archives are untrusted zip files. The server unpacks with limits, computes the h1 hash, reads the registry manifest for protocols, and maps each namespace to a registry-safe slug.

## Decisions

1. **Bounded unpack as structure.** Add an unexported unpack function that opens the staged zip, rejects absolute paths and dot-dot entries, enforces limits on unpacked size, file count, entry size and compression ratio, and returns the manifest bytes and the h1 digest.
2. **Archive truth from bytes.** Store H1 on registry.Archive at publish time from the unpack result, include H1 in SameContent with a missing-H1 rule for old releases, and derive protocols only from terraform-registry-manifest.json inside the archive.
3. **Stable slug from ID.** Derive the registry slug for each namespace from its stable ID in lowercase with unsafe characters removed, store the slug on the namespace record at publish time, index slug to namespace for lookup, and serve both the slug path and the prior name path for existing releases.

## Structure

Unexported type archiveFacts with constructor openArchiveFacts(stagedKey) that checks invariants; only the constructor unpacks bytes, and callers read H1 and protocols from its result.

## Risks

- Zip bomb exhausts disk or memory during unpack.
- Path traversal writes outside the staging directory.
- Publisher states false protocols and OpenTofu selects a provider it fails to start.
- Namespace name outside the safe subset breaks registry URLs.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Unpack rejects entries with absolute paths, dot-dot segments, and symlink bits, and stops past 64 MB unpacked size, 1024 files, 16 MB per file, and 100 times expansion of compressed size. | Publish a zip with ../../evil, absolute /tmp/evil, symlink entry, and zip bomb entries; each publish fails with 400 and no file leaves the staging directory. |
| service | Service compares stated protocols to manifest protocols with exact set match and refuses mismatch; a stated protocol value without a manifest fails closed. | Publish a provider that states protocols 5.0 while the manifest states 6.0, and one that states protocols without a manifest; the service returns invalid protocols and stores nothing. |
| HTTP handler | Registry HTTP handlers check publish authorization first, then zip content type, then count request bytes during read to 32 MB, and stage bytes only after those checks. | Send an oversize archive and a wrong content type with a principal without publish grant; the handler returns 403, 415, and 413 without unpacking. |
