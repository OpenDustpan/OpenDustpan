## ADDED Requirements

### Requirement: Tests pass with -trimpath

The tests of the module SHALL find their files from their package directory, so that they pass when `GOFLAGS` holds `-trimpath`.

#### Scenario: Trimpath build
- **WHEN** a maintainer runs `GOFLAGS=-trimpath go test ./internal/zonegen/... ./internal/cli/... ./internal/pulumijournal/...`
- **THEN** the tests pass

#### Scenario: No source lookup through the caller
- **WHEN** a test needs a file of the repository
- **THEN** the test joins a path relative to its package directory and does not call `runtime.Caller`
