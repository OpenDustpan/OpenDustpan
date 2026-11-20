# repository-checks Specification

## Purpose
The repository checks are the tests and scripts that guard the source tree. This capability covers how the tests of the module find their files, so that they pass in a build with -trimpath. It also covers `make files-check`, which refuses a large or binary file that a branch adds. It does not cover the content of the tests or the other build flags.

## Requirements

### Requirement: Tests pass with -trimpath

The tests of the module SHALL find their files from their package directory, so that they pass when `GOFLAGS` holds `-trimpath`.

#### Scenario: Trimpath build
- **WHEN** a maintainer runs `GOFLAGS=-trimpath go test ./internal/zonegen/... ./internal/cli/... ./internal/pulumijournal/...`
- **THEN** the tests pass

#### Scenario: No source lookup through the caller
- **WHEN** a test needs a file of the repository
- **THEN** the test joins a path relative to its package directory and does not call `runtime.Caller`

### Requirement: make files-check refuses large and binary files

`make files-check` SHALL fail when a file that the branch adds or changes against the merge base with the base ref is larger than 1 MiB, or holds a NUL byte in its first 8000 bytes outside a `testdata/` directory, unless `.large-files-allowlist` names it with a reason, and SHALL fail when git cannot list or read the files. CI SHALL run the check on each pull request and in the merge queue, so that each file that reaches main is checked.

#### Scenario: Large file
- **WHEN** a tracked file is larger than 1 MiB and not on the allowlist
- **THEN** the check fails and names the file and its size

#### Scenario: Binary file hidden by an attribute
- **WHEN** a binary file outside `testdata/` is added with a `.gitattributes` line that makes git diff it as text
- **THEN** the check fails and names the file

#### Scenario: Allowlisted file
- **WHEN** the allowlist names the file with a reason
- **THEN** the check passes

#### Scenario: Hostile allowlist line
- **WHEN** an allowlist line names a path with a `..` part
- **THEN** the check fails

#### Scenario: Git error
- **WHEN** the check runs outside a git repository
- **THEN** the check fails
