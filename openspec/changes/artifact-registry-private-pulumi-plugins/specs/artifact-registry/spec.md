## ADDED Requirements

### Requirement: Publish private Pulumi plugin archive set

When a namespace administrator publishes a Pulumi plugin archive set, the server SHALL check the SHA-256 values and the signature against the trusted keys of that namespace.

#### Scenario: Valid plugin publish
- **WHEN** a namespace administrator publishes a Pulumi plugin archive set with a registry-safe name
- **THEN** the server stores the archives with their SHA-256 values and signature verdict.

#### Scenario: Bad plugin name refused
- **WHEN** a publish uses uppercase letters or a leading dash in the plugin name
- **THEN** the server answers 400 that names the name rule.

### Requirement: Resolve plugin archive

When the Pulumi CLI requests the plugin download URL of a published release, the server SHALL stream the stored archive of that namespace, name, version, OS, and arch.

#### Scenario: Valid plugin install
- **WHEN** the Pulumi CLI requests the plugin download URL of a published plugin version for its OS and arch
- **THEN** the server streams the stored archive bytes with the stored SHA-256.

#### Scenario: Unknown OS or arch refused
- **WHEN** the Pulumi CLI requests an OS or arch with no stored archive
- **THEN** the server answers 404 that names the plugin, version, OS, and arch.

#### Scenario: Other namespace refused
- **WHEN** a caller requests a plugin URL outside its namespace
- **THEN** the server answers 404 without naming the stored release.

### Requirement: Authorize plugin download

When a caller requests a plugin download URL, the server SHALL require registry read access on that namespace.

#### Scenario: Missing token refused
- **WHEN** the download request has no valid access token
- **THEN** the server answers 401 that names opendustpan status and writes a refused-download audit entry.

#### Scenario: No read access refused
- **WHEN** the token lacks registry read access on the namespace
- **THEN** the server answers 404 to avoid leaking existence and writes a refused-download audit entry.

### Requirement: Bad version refused

When the requested plugin version has a failed signature verdict or a failed checksum verdict, the server SHALL refuse the download.

#### Scenario: Bad signature refused
- **WHEN** the Pulumi CLI requests a plugin version with a failed signature or checksum verdict
- **THEN** the server answers 403 that names the plugin and version and the publish API call, with no archive bytes.

### Requirement: Stream plugin archive

While the server streams a plugin archive, the server SHALL match the streamed bytes against the stored SHA-256.

#### Scenario: Corrupt blob stopped
- **WHEN** the stored bytes do not match the stored SHA-256 during the stream
- **THEN** the server aborts the connection with a truncated body and writes an audit entry for the failed download.

#### Scenario: Blob read failure stopped
- **WHEN** the blob store fails mid-stream after headers
- **THEN** the server aborts the connection and writes a failed-download audit entry with the plugin, version, OS, and arch.

#### Scenario: Successful download recorded
- **WHEN** the server streams a plugin archive
- **THEN** the server writes an audit entry with the principal, the namespace, the plugin, the version, and the verdict.
