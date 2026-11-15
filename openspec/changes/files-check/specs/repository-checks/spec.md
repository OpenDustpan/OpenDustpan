## ADDED Requirements

### Requirement: make files-check refuses large and binary files

`make files-check` SHALL fail when a tracked file is larger than 1 MiB or holds a NUL byte, unless `.large-files-allowlist` names it with a reason, and SHALL fail when git cannot list the files.

#### Scenario: Large file
- **WHEN** a tracked file is larger than 1 MiB and not on the allowlist
- **THEN** the check fails and names the file and its size

#### Scenario: Allowlisted file
- **WHEN** the allowlist names the file with a reason
- **THEN** the check passes

#### Scenario: Hostile allowlist line
- **WHEN** an allowlist line names a path with a `..` part
- **THEN** the check fails

#### Scenario: Git error
- **WHEN** the check runs outside a git repository
- **THEN** the check fails
