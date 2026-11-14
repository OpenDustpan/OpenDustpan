## ADDED Requirements

### Requirement: Yank release

When a caller with the manage grant yanks a published registry release, the server SHALL remove the version from the index and keep the download.

#### Scenario: Yank keeps download
- **WHEN** A caller with the manage grant yanks a published version with a reason.
- **THEN** The version leaves the index, the archive download still succeeds, and the record stays.

#### Scenario: Yanked plugin still downloads
- **WHEN** A client requests the plugin archive of a yanked version.
- **THEN** The Pulumi plugin download succeeds.

#### Scenario: Yank unknown version
- **WHEN** A caller yanks a version that is not present.
- **THEN** The server answers 404 with code registry.not-found.

#### Scenario: Yank without grant
- **WHEN** A caller without the manage grant yanks a version.
- **THEN** The server answers 403 with code forbidden.

#### Scenario: Repeated yank
- **WHEN** A caller yanks a version that is already yanked.
- **THEN** The server answers success and writes no new audit entry.

#### Scenario: Yank of revoked refused
- **WHEN** A caller yanks a version that is already revoked.
- **THEN** The server answers 409 with code registry.version-revoked.

### Requirement: Revoke release

When a caller with the manage grant revokes a registry release, the server SHALL block each later download of that version.

#### Scenario: Revoke blocks download
- **WHEN** A caller revokes a release with a reason and a tool then requests its archive.
- **THEN** The download answers 409 with code registry.version-revoked and the record stays.

#### Scenario: Revoked version missing from index
- **WHEN** A tool resolves versions after the revoke.
- **THEN** The mirror, module index, and plugin index omit the revoked version.

#### Scenario: Revoked plugin blocked
- **WHEN** A client requests the plugin archive of a revoked version.
- **THEN** The server answers 409 with code registry.version-revoked.

#### Scenario: Revoke unknown version
- **WHEN** A caller revokes a version that is not present.
- **THEN** The server answers 404 with code registry.not-found.

#### Scenario: Revoke without grant
- **WHEN** A caller without the manage grant revokes a version.
- **THEN** The server answers 403 with code forbidden.

#### Scenario: Repeated revoke
- **WHEN** A caller revokes a version that is already revoked.
- **THEN** The server answers success and writes no new audit entry.

### Requirement: Record life cycle move

When the server applies a yank or a revoke, the server SHALL write one audit entry for that move.

#### Scenario: Audit entry
- **WHEN** The server applies a yank or a revoke.
- **THEN** The audit log holds one entry with the principal, the release, the new state, and the reason.

### Requirement: List and show releases

When a caller with the read grant lists or shows registry releases, the server SHALL return only releases of the same namespace.

#### Scenario: List releases
- **WHEN** A caller with the read grant lists releases.
- **THEN** The server returns each release with name, version, and state in the same namespace.

#### Scenario: Include flag
- **WHEN** A caller lists releases after a yank and a revoke.
- **THEN** The default answer omits both and the answer with the include flag shows them with a state column.

#### Scenario: List without grant
- **WHEN** A caller without the read grant lists releases.
- **THEN** The server answers 403 with code forbidden.

#### Scenario: Show release
- **WHEN** A caller shows one release version.
- **THEN** The server returns the release with its state, checksums, and signature result.

#### Scenario: Show unknown version
- **WHEN** A caller shows a version that is not present.
- **THEN** The server answers 404 with code registry.not-found.

#### Scenario: Cross-namespace read refused
- **WHEN** A caller lists or shows a release of another namespace.
- **THEN** The server answers 404 with code registry.not-found.

### Requirement: List signing keys

When a caller with the read grant lists registry keys, the server SHALL return the trusted signing keys of the same namespace.

#### Scenario: List keys
- **WHEN** A caller with the read grant lists the registry keys.
- **THEN** The server returns each trusted key with its fingerprint.

#### Scenario: Keys list without grant
- **WHEN** A caller without the read grant lists registry keys.
- **THEN** The server answers 403 with code forbidden.
