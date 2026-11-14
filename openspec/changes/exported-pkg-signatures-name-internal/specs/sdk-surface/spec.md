## ADDED Requirements

### Requirement: No internal names in pkg

The SDK SHALL expose no exported pkg signature that names a type from internal.

#### Scenario: Third-party store build
- **WHEN** an outside module implements the NEW pkg/unitimport Store
- **THEN** the build passes and the gate reports no exported pkg signature that names internal.

### Requirement: Surface gate

While the baseline lists current leaks, the gate SHALL fail only on a new leak.

#### Scenario: New leak
- **WHEN** a change adds an exported pkg signature that names internal
- **THEN** make sdk-surface fails and names the new signature.
