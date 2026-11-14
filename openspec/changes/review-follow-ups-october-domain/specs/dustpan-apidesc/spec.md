## ADDED Requirements

### Requirement: Undescribed assembled route fails coverage

When a route mounts through server assembly without a description, the server SHALL fail the API coverage check.

#### Scenario: Extra undescribed route
- **WHEN** a test-only adapter with an extra undescribed route mounts through server assembly
- **THEN** coverage check fails for the extra route

#### Scenario: Described route passes
- **WHEN** a route is present in both operations and description
- **THEN** coverage check passes for that route

### Requirement: Concrete handler field discovery

When a struct field type implements http.Handler, the server SHALL report the field as unlisted until it is listed.

#### Scenario: Concrete handler field
- **WHEN** a fixture holds an unlisted concrete field that implements http.Handler
- **THEN** field discovery reports the concrete handler field while embedded traversal keeps working

### Requirement: Body declarations on both servers

When body declarations are checked, the server SHALL require them on both every-module and dev-module servers.

#### Scenario: Both server configs
- **WHEN** body assertions run against both server configurations
- **THEN** TestEveryOperationMarksItsBodies passes for both every-module and dev-module servers

#### Scenario: Dev-only gap
- **WHEN** a described dev-only operation lacks a required body declaration
- **THEN** the check fails for the described dev-only operation
