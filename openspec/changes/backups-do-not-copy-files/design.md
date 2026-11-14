## Context

Namespace backups copy registry rows but not the release files under the SHA-256 content keys. A restore on another blob store leaves releases that name missing files.

## Decisions

1. **Copy registry files by content key.** Extend the backup blob collection to read SHA-256 hashes from registry release rows and emit one blob reference per unique release file. Copy the files through the existing blob copy path. Validate the manifest and the SHA-256 of each file during restore before writing any file, and fail the restore when a file is missing or corrupt.
2. **Keep state version references unchanged.** Keep the MD5 BlobRef shape for state versions. Add a separate reference shape for registry files keyed by SHA-256, so old backups still read.
3. **Snapshot, scope and target location.** Read registry rows and the file list from one snapshot of the namespace. Copy only files named by published releases, not staged uploads. Write restored files under the target namespace ID, with one stored copy per unique SHA-256 key. A restore that stops midway leaves no partial registry files and a retry writes the same keys again.

## Structure

Unexported registryFileCollector built only by NewRegistryFileCollector with a namespace ID and a release reader; the constructor rejects an empty namespace ID and an empty hash.

## Risks

- Restore of an old backup without file entries still leaves missing files until the team re-publishes the releases.
- Large release files increase backup size and backup time.
