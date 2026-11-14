## ADDED Requirements

### Requirement: Publish provider version

When a principal with the publish grant on the namespace publishes a provider archive set, the server SHALL check the address, the SHA-256 list, the archive set membership, and the signature before it stores the release.

#### Scenario: Valid archive set
- **WHEN** A principal with the publish grant publishes an archive set with a valid SHA-256 list and a valid signature from a registered key.
- **THEN** The server stores one immutable release in the active state and lists the version in the mirror index.

#### Scenario: Bad SHA-256 at publish
- **WHEN** A publish carries a file whose SHA-256 differs from the list.
- **THEN** The server answers 422, names the file, and stores no release.

#### Scenario: Missing signature
- **WHEN** A publish carries no signature.
- **THEN** The server answers 422 and stores no release.

#### Scenario: Bad signature
- **WHEN** A publish carries a signature that no registered key of the same namespace trusts.
- **THEN** The server answers 422 and stores no release.

#### Scenario: Expired key at publish
- **WHEN** A publish carries a signature from an expired key.
- **THEN** The server answers 422 with registry.signature-untrusted-key and stores no release.

#### Scenario: File outside the archive set
- **WHEN** A publish names a file that the archive set does not list.
- **THEN** The server answers 400 and stores no release.

### Requirement: Namespace signing keys

The server SHALL trust only signing keys that the same namespace registered for provider signature checks.

#### Scenario: Register key
- **WHEN** A principal with the publish grant registers a signing key.
- **THEN** The server stores the armored OpenPGP key with its key ID for that namespace only.

#### Scenario: Malformed key
- **WHEN** A register call carries a key that is not a well-formed armored OpenPGP key.
- **THEN** The server answers 400 and stores no key.

#### Scenario: Duplicate key
- **WHEN** A register call repeats an existing key ID of the same namespace.
- **THEN** The server answers 409 and keeps the stored key unchanged.

#### Scenario: Oversize key
- **WHEN** A register call carries a key above the size limit.
- **THEN** The server answers 413 and stores no key.

#### Scenario: List keys
- **WHEN** A principal with the read grant requests the key list.
- **THEN** The server lists key IDs of the same namespace only.

#### Scenario: Cross-namespace key
- **WHEN** A publish in namespace A carries a signature that only a key of namespace B trusts.
- **THEN** The server refuses the publish with 422.

#### Scenario: Remove key
- **WHEN** A principal with the publish grant removes a signing key.
- **THEN** The server stops trusting the key for later publishes and keeps serving each version published before the removal.

### Requirement: Staged upload

When a principal with the publish grant on the namespace stages a provider file, the server SHALL enforce the configured size limit before it stores the staged blob.

#### Scenario: Stage file
- **WHEN** A principal with the publish grant uploads a file within the size limit.
- **THEN** The server stores the staged blob under a staging key and returns the key.

#### Scenario: Oversize staged file
- **WHEN** An upload carries a file above the size limit.
- **THEN** The server answers 413 and stores no blob.

### Requirement: Serve mirror index

When OpenTofu requests the provider mirror index for an address of the same namespace, the server SHALL list only active versions of that namespace.

#### Scenario: Known version
- **WHEN** A principal with the read grant requests the mirror index for a published address in the same namespace.
- **THEN** The server lists the version with its download address.

#### Scenario: Unknown version
- **WHEN** OpenTofu requests an address or version that the namespace never published.
- **THEN** The server answers 404 and names the address and version.

#### Scenario: Cross-namespace read
- **WHEN** A caller of namespace A requests an address published only in namespace B.
- **THEN** The server answers 404 and returns no data of namespace B.

### Requirement: Serve team provider protocol

When OpenTofu uses the team provider protocol for an address of the same namespace, the server SHALL return only active versions of that namespace with download metadata.

#### Scenario: Service discovery
- **WHEN** OpenTofu requests service discovery from the server host.
- **THEN** The server returns the provider registry endpoints for the server host.

#### Scenario: Versions list
- **WHEN** OpenTofu requests the versions document for a published address.
- **THEN** The server lists only active versions of the same namespace.

#### Scenario: Download metadata
- **WHEN** OpenTofu requests a published version of the same namespace.
- **THEN** The server returns the download address, the SHA-256 checksums, and the signature address.

#### Scenario: Unknown team version
- **WHEN** OpenTofu requests a version that the namespace never published.
- **THEN** The server answers 404 and names the address and version.

### Requirement: Serve provider download

When OpenTofu downloads a published provider file, the server SHALL verify the namespace match and the SHA-256 checksum before it serves the bytes.

#### Scenario: Valid download
- **WHEN** A principal with the read grant downloads a published file from the same namespace.
- **THEN** `tofu init` fetches the archive and its checksums verify.

#### Scenario: Bad checksum at serve
- **WHEN** A stored file fails its SHA-256 check at serve time.
- **THEN** The server answers 500, writes an audit entry, flags the file, and excludes the version from the index.

### Requirement: Host and safe address

The server SHALL refuse a provider address whose host differs exactly from the configured host or whose name breaks the registry-safe subset.

#### Scenario: Foreign host
- **WHEN** A publish names a host other than the configured server host.
- **THEN** The server refuses the publish with 400.

#### Scenario: Unsafe name
- **WHEN** A publish names a provider outside the registry-safe subset.
- **THEN** The server refuses the publish with 400.

#### Scenario: Traversal file name
- **WHEN** A publish names a file with a path separator or parent reference.
- **THEN** The server refuses the publish with 400 and stores no row.

### Requirement: Immutable versions

The server SHALL keep a published provider version immutable across republishes.

#### Scenario: Republish same files
- **WHEN** A principal repeats a publish with identical files for the same address and version.
- **THEN** The server answers success and changes no stored rows.

#### Scenario: Republish changed files
- **WHEN** A principal repeats a publish with different files for the same address and version.
- **THEN** The server refuses the publish with 409 as immutable.

#### Scenario: Parallel publish
- **WHEN** Two publishes with different files for the same new version run at the same time.
- **THEN** One publish succeeds and the other gets 409 with no orphan blob.

### Requirement: Authorization and audit

When a principal publishes a provider or changes a signing key, the server SHALL require the publish grant on the namespace and write an audit entry.

#### Scenario: Denied publish
- **WHEN** A principal without the publish grant attempts a publish or a key change.
- **THEN** The server answers 403 and writes an audit entry.

#### Scenario: Recorded publish
- **WHEN** A publish or a key change succeeds.
- **THEN** The audit log holds who published which address and version and when.
