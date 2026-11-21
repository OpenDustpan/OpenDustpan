## ADDED Requirements

### Requirement: Archive set

When an operator builds a release, the server SHALL produce four tar.gz archives for linux/amd64, linux/arm64, darwin/amd64, and darwin/arm64 with a SHA256SUMS file.

#### Scenario: Four archives present
- **WHEN** an operator builds archives for one version
- **THEN** the output holds opendustpan_<version>_linux_amd64.tar.gz, opendustpan_<version>_linux_arm64.tar.gz, opendustpan_<version>_darwin_amd64.tar.gz, opendustpan_<version>_darwin_arm64.tar.gz, plus SHA256SUMS

### Requirement: Reproducible archives

When an operator builds one version twice from a clean tree, the server SHALL produce byte equal archives.

#### Scenario: Byte equal rebuild
- **WHEN** an operator builds one version twice from a clean tree
- **THEN** both runs give byte equal archives and SHA256SUMS entries

### Requirement: Version stamp

When an operator builds archives for a version, the server SHALL stamp each binary with that version.

#### Scenario: Stamped version check
- **WHEN** an operator checks the stamp of each archive binary
- **THEN** each binary reports the given version from its version flag or its embedded version string

### Requirement: License files

When an operator builds archives, the server SHALL include the root LICENSE file and the third party license file in each archive.

#### Scenario: License contents check
- **WHEN** an operator lists the files of each archive
- **THEN** each archive holds the root LICENSE file and the third party license file

### Requirement: Record check first

When an operator builds a release, the server SHALL run the release record check for the version before any build, and SHALL refuse the build when the record has no line for the version.

#### Scenario: Version missing from the record
- **WHEN** an operator builds a version that the release record does not list
- **THEN** the build refuses with the record message and writes no archive

### Requirement: Version input allowlist

If an operator gives a version outside the form v followed by three dot separated numbers, then the server SHALL refuse the build with a bad version error.

#### Scenario: Bad version refusal
- **WHEN** an operator gives a version outside the form v followed by three dot separated numbers
- **THEN** the build refuses the value with a bad version error and writes no file

### Requirement: Verify checksums and files

When an archive checksum, file set, license set, or version stamp does not match, the server SHALL refuse verification with the matching error.

#### Scenario: Corrupt archive check
- **WHEN** an archive file differs from its SHA256SUMS entry
- **THEN** verification refuses the set with a checksum error

#### Scenario: Missing file check
- **WHEN** an expected archive or license file is absent from the output directory
- **THEN** verification refuses the set with a missing file error

#### Scenario: Stamp mismatch check
- **WHEN** one binary carries an embedded version different from the given version
- **THEN** verification refuses the set with a stamp error

### Requirement: Hostile archive refusal

When an archive holds an entry outside the allowlist of the binary, LICENSE, README.md and the third_party_licenses directory, the server SHALL refuse verification with an archive content error.

#### Scenario: Traversal entry check
- **WHEN** an archive holds an absolute path, a dot dot entry, or a file outside the allowlist of the binary, LICENSE, README.md and the third_party_licenses directory
- **THEN** verification refuses the set with an archive content error
