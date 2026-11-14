## ADDED Requirements

### Requirement: fail-on-leaked-goroutine

When a test in pkg/forgelink leaves a goroutine running, the test binary SHALL fail the package.

#### Scenario: leaking goroutine fails package
- **WHEN** a test in pkg/forgelink leaves a goroutine running after the test ends
- **THEN** the test run of pkg/forgelink fails at TestMain.

#### Scenario: clean package passes
- **WHEN** no test in pkg/forgelink leaves a goroutine running
- **THEN** the test run of pkg/forgelink passes.
