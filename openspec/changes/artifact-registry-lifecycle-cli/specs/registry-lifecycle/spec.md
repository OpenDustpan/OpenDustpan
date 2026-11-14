## ADDED Requirements

### Requirement: Yank release

When a caller yanks a published registry release, the server SHALL remove the version from the index and keep the download.

#### Scenario: Yank keeps download
- **WHEN** A caller with the manage grant yanks a published version.
- **THEN** The version leaves the index, the archive download still succeeds, and the record stays.

#### Scenario: Yanked version missing from index
- **WHEN** A tool resolves a version range after the yank.
- **THEN** The provider and module index answers omit the yanked version.

#### Scenario: Yank unknown version
- **WHEN** A caller yanks a version that is not present.
- **THEN** The server answers 404 with code registry.version-not-found.

#### Scenario: Yank without grant
- **WHEN** A caller without the manage grant yanks a version.
- **THEN** The server answers 403 with code forbidden.

#### Scenario: Repeated yank
- **WHEN** A caller yanks a version that is already yanked.
- **THEN** The server answers success and the state, index, and download stay the same.

### Requirement: Revoke release

When a caller revokes a registry release, the server SHALL block each later download of that version.

#### Scenario: Revoke blocks download
- **WHEN** A caller with the manage grant revokes a release and a tool then requests its archive.
- **THEN** Each later archive download answers 409 with code registry.version-revoked and the record stays.

#### Scenario: Revoked version missing from index
- **WHEN** A tool resolves versions after the revoke.
- **THEN** The provider network mirror, the module index, and the plugin download index omit the revoked version.

#### Scenario: Revoke unknown version
- **WHEN** A caller revokes a version that is not present.
- **THEN** The server answers 404 with code registry.version-not-found.

#### Scenario: Revoke without grant
- **WHEN** A caller without the manage grant revokes a version.
- **THEN** The server answers 403 with code forbidden.

#### Scenario: Repeated revoke
- **WHEN** A caller revokes a version that is already revoked.
- **THEN** The server answers success and the blocked download stays blocked.

### Requirement: Record life cycle move

When the server applies a yank or a revoke, the server SHALL write one audit entry for that move.

#### Scenario: Audit entry for yank and revoke
- **WHEN** The server applies a yank or a revoke.
- **THEN** The audit log holds one entry with the principal, the release, the new state, and the reason.

### Requirement: List and show releases

When a caller lists or shows registry releases, the server SHALL return only releases of the same namespace.

#### Scenario: List releases
- **WHEN** A caller with the read grant lists provider or module releases.
- **THEN** The server returns each release with name, version, and state in the same namespace.

#### Scenario: List hides moved versions by default
- **WHEN** A caller lists releases after a yank and a revoke.
- **THEN** The default answer omits yanked and revoked versions and the answer with the include flag shows them with a state column.

#### Scenario: Show release
- **WHEN** A caller with the read grant shows one release version.
- **THEN** The server returns the release with its state, checksums, and signature result.

#### Scenario: Cross-namespace read refused
- **WHEN** A caller lists or shows a release of another namespace.
- **THEN** The server answers 404 or 403 and returns no release of the other namespace.

### Requirement: List signing keys

When a caller lists registry keys, the server SHALL return the trusted signing keys of the same namespace.

#### Scenario: List keys
- **WHEN** A caller with the read grant lists the registry keys of the namespace.
- **THEN** The server returns each trusted key with fingerprint and algorithm.
