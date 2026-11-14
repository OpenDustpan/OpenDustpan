## Context

Private Pulumi plugin download reuses the publish path, per-namespace trusted keys, and stored checksum and signature verdicts of earlier registry parts.

## Decisions

1. **Reuse publish record for plugin download.** Reuse the same release record and blob reference used for providers: publish stores archives and verdicts once, download resolves namespace, name, version, OS and arch to one stored archive and streams its bytes after the stored verdict check and a SHA-256 match of the bytes.
2. **Yanked downloadable and revoked blocked.** Keep yanked versions downloadable for pinned installs and block revoked versions at download, pending the owner answer on the four state names.

## Structure

Unexported type pluginDownload with unexported fields for namespace ID, release ID, and verified digest; only its constructor builds a value from a verified release row and the constructor refuses a row with a failed verdict.

## Risks

- A bad archive reaches the Pulumi CLI when download skips the stored verdict check.
- One namespace reads the plugin of another namespace through a guessed URL.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service resolves only releases of the request namespace and refuses unknown name, version, OS, or arch with 404. | Request a plugin from namespace B with a URL of namespace A and show 404. |
| HTTP handler | Download handler checks the stored checksum and signature verdict before the first byte and refuses a bad or revoked version with 403 that names the artifact. | Mark a stored version bad or revoked, request its bytes direct, and show 403 with no bytes sent. |
| HTTP handler | Download handler compares the SHA-256 of the stored bytes with the stored checksum and stops the stream on mismatch. | Corrupt one byte of the stored blob, request the download, and show a failed stream with no complete archive. |
