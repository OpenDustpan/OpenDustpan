## ADDED Requirements

### Requirement: Publish module version

When a platform team uploads a module archive set, the server SHALL check checksums and trusted signature at upload before it marks the version as served.

#### Scenario: Accept module archive set
- **WHEN** Platform team uploads a module archive set with valid checksums and trusted signature
- **THEN** Server stores the release and marks the version as served.

#### Scenario: Refuse bad module upload
- **WHEN** Upload carries a bad checksum, an untrusted signature, or a name outside the registry-safe subset
- **THEN** Server refuses the upload with an error that names the failed input.

### Requirement: Serve discovery document

When a caller requests the well-known discovery document, the server SHALL return modules.v1 that points at the module registry routes.

#### Scenario: Discover module registry
- **WHEN** tofu init requests the well-known discovery document
- **THEN** Server returns a discovery document that lists modules.v1 for the server host.

### Requirement: Serve module version list

Where a module has served versions in the caller namespace, the server SHALL return only served versions through the module registry protocol.

#### Scenario: List versions of published module
- **WHEN** tofu init requests versions of a published module in the caller namespace
- **THEN** Server returns only served versions with their download source addresses.

#### Scenario: Hide unserved versions
- **WHEN** A module has versions that are not marked as served
- **THEN** Server omits each unpublished or blocked version from the list.

### Requirement: Serve module download

When a caller with read grant requests the download source of a served module version, the server SHALL return the stored archive of that version.

#### Scenario: Download published module archive
- **WHEN** Authenticated caller with read grant requests the download source of a served module version
- **THEN** Server returns the stored archive of the requested version without added signature.

#### Scenario: Refuse download of unserved version
- **WHEN** Caller requests a version that is not marked as served
- **THEN** Server answers 404 with error code module.not-found.

### Requirement: Refuse cross-namespace module read

If a module address names another namespace than the caller namespace, then the server SHALL answer with the same not-found answer as a missing module.

#### Scenario: Request module from another namespace
- **WHEN** A caller requests a module address of another namespace
- **THEN** Server answers 404 with error code module.not-found and writes an audit entry.

### Requirement: Refuse wrong host and bad name

If a module address names a wrong host or a malformed name, then the server SHALL deny the request with a not-found answer.

#### Scenario: Request with wrong host
- **WHEN** Module address names a host other than the configured host
- **THEN** Server answers 404 with error code module.not-found.

#### Scenario: Request with malformed name
- **WHEN** Module address carries a name outside the registry-safe subset
- **THEN** Server answers 404 with error code module.not-found.

### Requirement: Require read grant and audit

When a caller without read grant on the namespace registry requests a module, the server SHALL deny the request and write an audit entry.

#### Scenario: Deny caller without grant
- **WHEN** Caller without read grant on the namespace registry requests module versions or download
- **THEN** Server answers 403 with error code registry.forbidden and writes an audit entry.

#### Scenario: Deny unauthenticated caller
- **WHEN** Request carries no valid credential
- **THEN** Server answers 401 and writes an audit entry.
