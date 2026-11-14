## ADDED Requirements

### Requirement: refuse-duplicate-error-variant

When two entries produce the same generated error-variant name with exact case-sensitive equality before schema build, the server SHALL refuse startup.

#### Scenario: Duplicate generated name
- **WHEN** Two entries produce the same generated error-variant name
- **THEN** Startup fails and names the duplicate generated error-variant name.

#### Scenario: Normalization collision
- **WHEN** Two different source entries normalize to the same generated name with exact case-sensitive equality
- **THEN** Startup fails and names the shared generated name.

#### Scenario: Three entries with one duplicate
- **WHEN** Three entries hold two copies of one generated name
- **THEN** Startup fails and names the repeated generated name.

#### Scenario: Unique names pass
- **WHEN** All generated error-variant names are unique
- **THEN** The API description build succeeds and the catalog match test passes.

### Requirement: propagate-duplicate-error

When checkedErrorVariants finds a duplicate generated name, the server SHALL return the error through WithErrorVariants to the API description build.

#### Scenario: Error reaches startup
- **WHEN** checkedErrorVariants finds a duplicate generated name
- **THEN** The API description build returns the duplicate-name error and server start fails.
