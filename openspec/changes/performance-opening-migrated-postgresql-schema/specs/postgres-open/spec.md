## ADDED Requirements

### Requirement: refuse-tampered-migrations

The server SHALL refuse PostgreSQL open when a recorded migration row differs from the embedded migration.

#### Scenario: tampered-name-refuses
- **WHEN** A recorded migration name differs from the embedded migration name.
- **THEN** Open refuses with the same error text as today.

#### Scenario: tampered-sha256-refuses
- **WHEN** A recorded migration sha256 differs from the embedded migration sha256.
- **THEN** Open refuses with the same error text as today.

#### Scenario: unknown-version-refuses
- **WHEN** A recorded migration version has no embedded migration.
- **THEN** Open refuses with the same error text as today.

#### Scenario: duplicate-version-refuses
- **WHEN** Recorded rows hold the same version twice.
- **THEN** Open refuses with the same error text as today.

#### Scenario: null-sha256-refuses
- **WHEN** A recorded migration sha256 is NULL.
- **THEN** Open refuses with the same error text as today.
