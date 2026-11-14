## ADDED Requirements

### Requirement: single-wait-loop

The server SHALL keep exactly one definition of endGroupAfterExit in internal/clientgen/conformance with no build tag.

#### Scenario: single-wait-loop
- **WHEN** a check lists definitions of endGroupAfterExit in internal/clientgen/conformance
- **THEN** the package defines endGroupAfterExit exactly once in a file with no build tag

#### Scenario: no-other-platform-file
- **WHEN** a check lists build tags in internal/clientgen/conformance
- **THEN** no file in the package carries a negated build tag for other platforms
