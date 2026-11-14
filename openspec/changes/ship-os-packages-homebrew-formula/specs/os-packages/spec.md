## ADDED Requirements

### Requirement: Package contents

The release SHALL ship deb, rpm and apk packages that hold the binary and the license files.

#### Scenario: Install in a container
- **WHEN** a CI step installs a package in a clean container and runs `opendustpan --version`
- **THEN** the command prints the version and the license files exist

### Requirement: Hash check

If a package or archive hash differs from its SHA256SUMS entry, then the publish step SHALL refuse to publish.

#### Scenario: Hash mismatch
- **WHEN** the hash of an archive differs from SHA256SUMS
- **THEN** the publish step fails and names the expected hash

#### Scenario: Missing entry
- **WHEN** SHA256SUMS has no entry for an archive
- **THEN** the publish step fails

### Requirement: Upgrade and removal

When the Operator removes the package, the package manager SHALL delete the binary.

#### Scenario: Remove
- **WHEN** the Operator removes the package
- **THEN** the binary is not on the path
